#!/usr/bin/env ruby
require 'json'
require 'open3'
require 'shellwords'
require 'fiddle'

module ReleaseKeychain
  module_function

  def default_status
    security = Fiddle.dlopen('/System/Library/Frameworks/Security.framework/Security')
    core = Fiddle.dlopen('/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation')
    copy = Fiddle::Function.new(security['SecKeychainCopyDomainDefault'], [Fiddle::TYPE_INT, Fiddle::TYPE_VOIDP], Fiddle::TYPE_INT)
    release = Fiddle::Function.new(core['CFRelease'], [Fiddle::TYPE_VOIDP], Fiddle::TYPE_VOID)
    pointer = Fiddle::Pointer.malloc(Fiddle::SIZEOF_VOIDP)
    pointer[0, Fiddle::SIZEOF_VOIDP] = [0].pack('J')
    status = copy.call(0, pointer)
    reference = pointer[0, Fiddle::SIZEOF_VOIDP].unpack1('J')
    release.call(reference) unless reference.zero?
    status
  end

  def query(*arguments)
    output, status = Open3.capture2('security', *arguments)
    abort 'Unable to read runner Keychain state' unless status.success?
    Shellwords.shellsplit(output)
  end

  def run(mode, path)
    abort 'Usage: release-keychain.rb save|restore STATE_FILE' unless path && %w[save restore].include?(mode)
    if mode == 'save'
      # The security CLI collapses OSStatus to exit 1, hiding an absent default behind other errors.
      status = default_status
      default = case status
                when 0 then query('default-keychain', '-d', 'user').fetch(0)
                when -25307 then nil # errSecNoDefaultKeychain on a fresh service account.
                else abort "Unable to read default Keychain (OSStatus #{status})"
                end
      state = {'default' => default, 'search' => query('list-keychains', '-d', 'user')}
      File.open(path, File::WRONLY | File::CREAT | File::EXCL, 0600) { |file| file.write(JSON.generate(state)) }
    else
      state = JSON.parse(File.read(path))
      arguments = ['security', 'default-keychain', '-d', 'user', '-s']
      arguments << state.fetch('default') unless state.fetch('default').nil?
      abort 'Unable to restore default Keychain' unless system(*arguments)
      abort 'Unable to restore Keychain search list' unless system('security', 'list-keychains', '-d', 'user', '-s', *state.fetch('search'))
      File.delete(path)
    end
  end
end

ReleaseKeychain.run(*ARGV) if $PROGRAM_NAME == __FILE__
