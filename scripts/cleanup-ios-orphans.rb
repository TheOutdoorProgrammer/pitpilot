#!/usr/bin/env ruby
# Recover only abandoned CI helpers whose process, checkout and completed run agree.
require 'json'
require 'net/http'
require 'open3'
require 'shellwords'

class IOSOrphanSystem
  def capture(*command)
    Open3.popen3(*command) do |input, output, error, waiter|
      input.close
      readers = [Thread.new { output.read }, Thread.new { error.read }]
      unless waiter.join(10)
        Process.kill('KILL', waiter.pid) rescue Errno::ESRCH
        waiter.join
        readers.each(&:join)
        raise 'command timed out'
      end
      [readers[0].value, waiter.value.success?]
    end
  end

  def processes
    text, ok = capture('ps', '-U', Process.uid.to_s, '-o', 'pid=,ppid=,comm=')
    raise 'process list unavailable' unless ok
    text.lines.map do |line|
      pid, parent, command = line.strip.split(/\s+/, 3)
      { pid: pid.to_i, parent: parent.to_i, command: command } if command
    end.compact
  end

  def snapshot(pid)
    text, ok = capture('ps', '-p', pid.to_s, '-o', 'pid=,uid=,ppid=,stat=,lstart=,comm=')
    return nil unless ok
    fields = text.strip.split(/\s+/, 10)
    return nil unless fields.length == 10
    arguments, args_ok = capture('ps', '-p', pid.to_s, '-o', 'args=')
    directory, cwd_ok = capture('lsof', '-a', '-p', pid.to_s, '-d', 'cwd', '-Fn')
    return nil unless args_ok && cwd_ok
    { pid: fields[0].to_i, uid: fields[1].to_i, parent: fields[2].to_i,
      state: fields[3], started: fields[4..8].join(' '), command: fields[9],
      arguments: arguments.strip, cwd: directory.lines.find { |line| line.start_with?('n') }&.strip&.delete_prefix('n') }
  end

  def devices
    text, ok = capture('xcrun', 'simctl', 'list', 'devices', '--json')
    raise 'simulator list unavailable' unless ok
    JSON.parse(text).fetch('devices').values.flatten
  end

  def completed_run?(repository, run_id)
    uri = URI("https://api.github.com/repos/#{repository}/actions/runs/#{run_id}")
    request = Net::HTTP::Get.new(uri)
    request['Accept'] = 'application/vnd.github+json'
    request['User-Agent'] = 'PitPilot-native-cleanup'
    response = Net::HTTP.start(uri.host, uri.port, use_ssl: true, open_timeout: 5, read_timeout: 5) { |http| http.request(request) }
    unless response.is_a?(Net::HTTPSuccess)
      warn "Skipping orphan cleanup: GitHub run lookup returned HTTP #{response.code}."
      return false
    end
    run = JSON.parse(response.body)
    run['id'].to_s == run_id && run.dig('repository', 'full_name') == repository && run['status'] == 'completed'
  end

  def signal(signal, process)
    Process.kill(signal, process.fetch(:pid))
  rescue Errno::ESRCH
    nil
  end

  def pause
    sleep 0.1
  end

  def remove_device(id)
    capture('xcrun', 'simctl', 'shutdown', id)
    _, ok = capture('xcrun', 'simctl', 'delete', id)
    raise 'owned simulator removal failed' unless ok
  end
end

class IOSOrphanCleanup
  def initialize(system, checkout:, repository:, current_run:, uid: Process.uid)
    @system, @checkout, @repository, @current_run, @uid = system, checkout, repository, current_run, uid
  end

  def same_process?(expected)
    current = @system.snapshot(expected.fetch(:pid))
    current && !current.fetch(:state).start_with?('Z') &&
      current.reject { |key, _| key == :state } == expected.reject { |key, _| key == :state }
  end

  def candidate(process, processes)
    return nil unless process[:parent] == 1 && File.basename(process[:command]) == 'bash'
    helper = @system.snapshot(process[:pid])
    return nil unless helper && helper[:uid] == @uid && helper[:parent] == 1 && helper[:cwd] == @checkout
    arguments = Shellwords.split(helper[:arguments])
    return nil unless arguments.length == 2 && %w[bash /bin/bash /usr/bin/bash].include?(arguments[0]) &&
                      File.expand_path(arguments[1], @checkout) == File.join(@checkout, 'scripts/test-ios.sh')
    children = processes.select { |child| child[:parent] == helper[:pid] }
    return nil unless children.length == 1 && File.basename(children[0][:command]) == 'xcodebuild'
    child = @system.snapshot(children[0][:pid])
    return nil unless child && child[:uid] == @uid && child[:parent] == helper[:pid] && child[:cwd] == @checkout
    destination = child[:arguments].match(/-destination ['"]?platform=iOS Simulator,id=([0-9A-Fa-f-]{36})(?:['"]?\s|$)/)
    return nil unless destination && child[:arguments].include?('-project ios/PitPilot.xcodeproj') &&
                      child[:arguments].include?('-scheme PitPilot')
    device = @system.devices.find { |item| item['udid'] == destination[1] }
    identity = device && device['name'].match(/\APitPilot-(\d+)-(\d+)\z/)
    return nil unless identity && identity[1] != @current_run
    return nil unless @system.completed_run?(@repository, identity[1])
    [helper, child, device['udid'], identity[1]]
  rescue ArgumentError
    nil
  end

  def recover(helper, child, device, run_id)
    return unless same_process?(helper) && same_process?(child)
    @system.signal('TERM', child)
    @system.signal('TERM', helper) if same_process?(helper)
    30.times do
      break unless same_process?(child) || same_process?(helper)
      @system.pause
    end
    [child, helper].each { |process| @system.signal('KILL', process) if same_process?(process) }
    20.times do
      break unless same_process?(child) || same_process?(helper)
      @system.pause
    end
    if same_process?(child) || same_process?(helper)
      warn 'Skipping simulator removal: an owned process has not exited.'
      return
    end
    # The helper's EXIT trap may already have deleted its simulator.
    @system.remove_device(device) if @system.devices.any? { |item| item['udid'] == device }
    puts "Recovered abandoned native test resources from completed run #{run_id}."
  end

  def run
    return unless @repository&.match?(/\A[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+\z/) && @current_run&.match?(/\A\d+\z/)
    processes = @system.processes
    processes.select { |process| process[:parent] == 1 && File.basename(process[:command]) == 'bash' }.first(4).each do |process|
      match = candidate(process, processes)
      recover(*match) if match
    end
  rescue StandardError => error
    # Isolation still works when ownership or the completed-run check is unavailable.
    warn "Skipping orphan cleanup: #{error.class}."
  end
end

if $PROGRAM_NAME == __FILE__
  IOSOrphanCleanup.new(IOSOrphanSystem.new, checkout: File.realpath(File.join(__dir__, '..')),
                      repository: ENV['GITHUB_REPOSITORY'], current_run: ENV['GITHUB_RUN_ID']).run
end
