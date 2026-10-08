#!/usr/bin/env ruby
require 'minitest/autorun'
require 'tmpdir'
require 'fileutils'
require 'timeout'
require_relative 'cleanup-ios-orphans'

class FakeOrphanSystem
  attr_reader :signals, :removed
  attr_accessor :completed, :items, :simulators

  def initialize
    common = { uid: 501, state: 'S', started: 'Thu Oct 8 17:00:00 2026', cwd: '/checkout' }
    @items = [common.merge(pid: 20, parent: 1, command: 'bash', arguments: 'bash scripts/test-ios.sh'),
              common.merge(pid: 21, parent: 20, command: '/Xcode/xcodebuild',
                           arguments: '/Xcode/xcodebuild test -project ios/PitPilot.xcodeproj -scheme PitPilot -destination platform=iOS Simulator,id=11111111-2222-3333-4444-555555555555')]
    @simulators = [{ 'name' => 'PitPilot-123-1', 'udid' => '11111111-2222-3333-4444-555555555555' }]
    @completed = true
    @signals, @removed = [], []
  end

  def processes; @items; end
  def snapshot(pid); @items.find { |item| item[:pid] == pid }; end
  def devices; @simulators; end
  def completed_run?(repository, run); @completed && repository == 'owner/repo' && run == '123'; end
  def pause; end
  def signal(signal, process)
    @signals << [signal, process[:pid]]
    @items = @items.reject { |item| item[:pid] == process[:pid] }
  end
  def remove_device(id); @removed << id; end
end

class IOSOrphanCleanupTest < Minitest::Test
  def setup
    @system = FakeOrphanSystem.new
    @cleanup = IOSOrphanCleanup.new(@system, checkout: '/checkout', repository: 'owner/repo', current_run: '456', uid: 501)
  end

  def test_only_verified_completed_run_is_recovered
    capture_io { @cleanup.run }
    assert_equal [['TERM', 21], ['TERM', 20]], @system.signals
    assert_equal ['11111111-2222-3333-4444-555555555555'], @system.removed
  end

  def test_active_run_is_not_touched
    @system.completed = false
    @cleanup.run
    assert_empty @system.signals
  end

  def test_wrong_repository_is_not_touched
    IOSOrphanCleanup.new(@system, checkout: '/checkout', repository: 'different/repo', current_run: '456', uid: 501).run
    assert_empty @system.signals
  end

  def test_current_run_is_not_touched
    @system.simulators[0]['name'] = 'PitPilot-456-1'
    @cleanup.run
    assert_empty @system.signals
  end

  def test_unmatched_or_non_ci_simulator_is_not_touched
    @system.simulators[0]['udid'] = 'AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE'
    @cleanup.run
    assert_empty @system.signals
    @system.simulators[0]['udid'] = '11111111-2222-3333-4444-555555555555'
    @system.simulators[0]['name'] = 'Someone-elses-simulator'
    @cleanup.run
    assert_empty @system.signals
  end

  def test_foreground_helper_is_not_touched
    @system.items[0][:parent] = 10
    @cleanup.run
    assert_empty @system.signals
  end

  def test_other_uid_or_checkout_is_not_touched
    @system.items[0][:uid] = 502
    @cleanup.run
    assert_empty @system.signals
    @system.items[0][:uid] = 501
    @system.items[1][:cwd] = '/other-checkout'
    @cleanup.run
    assert_empty @system.signals
  end

  def test_extra_child_or_different_script_is_not_touched
    @system.items << @system.items[1].merge(pid: 22)
    @cleanup.run
    assert_empty @system.signals
    @system.items.pop
    @system.items[0][:arguments] = 'bash scripts/release-ios.sh'
    @cleanup.run
    assert_empty @system.signals
  end

  def test_pid_reuse_or_zombie_prevents_signal
    original = @system.items[0].dup
    @system.items[0][:started] = 'Thu Oct 8 18:00:00 2026'
    refute @cleanup.same_process?(original)
    @system.items[0][:state] = 'Z'
    refute @cleanup.same_process?(@system.items[0])
  end

  def test_api_failure_skips_safely
    def @system.completed_run?(*); raise IOError; end
    _, warning = capture_io { @cleanup.run }
    assert_match(/Skipping orphan cleanup: IOError/, warning)
    assert_empty @system.signals
  end
end

class IOSRunnerLifecycleTest < Minitest::Test
  STUB = <<~'SH'
    #!/bin/bash
    set -eu
    printf '%s %s\n' "${0##*/}" "$*" >> "$FIXTURE_LOG"
    if [[ "${0##*/}" == xcodebuild ]]; then
      if [[ "$1" == build-for-testing && "${FIXTURE_FAIL_BUILD:-}" == 1 ]]; then exit 65; fi
      if [[ "$1" == "${FIXTURE_BLOCK:-none}" ]]; then
        echo $$ > "$FIXTURE_PID"
        exec /bin/sleep 300
      fi
      exit 0
    fi
    case "$2 $3" in
      'list runtimes') printf '%s\n' '{"runtimes":[{"isAvailable":true,"version":"26.5","identifier":"test-runtime","supportedDeviceTypes":[{"identifier":"com.apple.CoreSimulator.SimDeviceType.iPhone-17"}]}]}' ;;
      'create '*) echo 11111111-2222-3333-4444-555555555555 ;;
      'bootstatus '*)
        if [[ "${FIXTURE_BLOCK:-}" == bootstatus ]]; then
          echo $$ > "$FIXTURE_PID"
          exec /bin/sleep 300
        fi
        ;;
    esac
  SH

  def setup
    @root = Dir.mktmpdir('pitpilot-shell-fixture-')
    @bin = File.join(@root, 'bin')
    FileUtils.mkdir_p(@bin)
    %w[xcodebuild xcrun].each do |tool|
      path = File.join(@bin, tool)
      File.write(path, STUB)
      File.chmod(0o700, path)
    end
    @env = { 'PATH' => "#{@bin}:#{ENV.fetch('PATH')}", 'RUNNER_TEMP' => @root,
             'GITHUB_RUN_ID' => '987', 'GITHUB_RUN_ATTEMPT' => '2', 'GITHUB_REPOSITORY' => nil,
             'FIXTURE_LOG' => File.join(@root, 'calls'), 'FIXTURE_PID' => File.join(@root, 'child') }
    @helper = File.join(__dir__, 'test-ios.sh')
  end

  def teardown
    FileUtils.remove_entry(@root)
  end

  def run_helper(extra = {})
    output = File.join(@root, 'output')
    pid = Process.spawn(@env.merge(extra), '/bin/bash', @helper, out: output, err: output, pgroup: true)
    yield pid if block_given?
    _, status = Timeout.timeout(15) { Process.wait2(pid) }
    status
  ensure
    Process.kill('KILL', -pid) rescue Errno::ESRCH if pid
  end

  def calls
    File.read(@env.fetch('FIXTURE_LOG'))
  end

  def test_build_then_full_test_uses_owned_paths
    assert run_helper.success?
    assert_operator calls.index('build-for-testing'), :<, calls.index('bootstatus')
    assert_includes calls, 'test-without-building'
    assert_includes calls, '/pitpilot-derived-data/987-2-'
    assert_includes calls, '/pitpilot-test-results/987-2/'
    assert_includes calls, 'simctl delete 11111111-2222-3333-4444-555555555555'
    assert_empty Dir.glob(File.join(@root, 'pitpilot-derived-data', '*'))
  end

  def test_build_failure_never_boots_simulator
    assert_equal 65, run_helper('FIXTURE_FAIL_BUILD' => '1').exitstatus
    refute_includes calls, 'bootstatus'
    assert_includes calls, 'simctl delete 11111111-2222-3333-4444-555555555555'
  end

  def assert_cancellation(phase, signal = 'TERM', expected_status = 143)
    child = nil
    status = run_helper('FIXTURE_BLOCK' => phase) do |pid|
      Timeout.timeout(10) { sleep 0.02 until File.exist?(@env.fetch('FIXTURE_PID')) }
      child = Integer(File.read(@env.fetch('FIXTURE_PID')))
      Process.kill(signal, pid)
    end
    assert_equal expected_status, status.exitstatus
    assert_raises(Errno::ESRCH) { Process.kill(0, child) }
    assert_includes calls, 'simctl delete 11111111-2222-3333-4444-555555555555'
    assert_empty Dir.glob(File.join(@root, 'pitpilot-derived-data', '*'))
  ensure
    Process.kill('KILL', child) rescue Errno::ESRCH if child
  end

  def test_cancellation_reaches_build_child
    assert_cancellation('build-for-testing')
  end

  def test_cancellation_reaches_boot_child
    assert_cancellation('bootstatus')
  end

  def test_cancellation_reaches_test_child
    assert_cancellation('test-without-building')
  end

  def test_interrupt_reaches_test_child
    assert_cancellation('test-without-building', 'INT', 130)
  end
end
