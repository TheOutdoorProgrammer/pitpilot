#!/usr/bin/env ruby
require 'json'
require 'open3'
require 'shellwords'

mode, path = ARGV
abort 'Usage: release-keychain.rb save|restore STATE_FILE' unless path && %w[save restore].include?(mode)

def query(*arguments)
  output, status = Open3.capture2('security', *arguments)
  abort 'Unable to read runner Keychain state' unless status.success?
  Shellwords.shellsplit(output)
end

if mode == 'save'
  state = {
    'default' => query('default-keychain', '-d', 'user').fetch(0),
    'search' => query('list-keychains', '-d', 'user')
  }
  File.open(path, File::WRONLY | File::CREAT | File::EXCL, 0600) { |file| file.write(JSON.generate(state)) }
else
  state = JSON.parse(File.read(path))
  abort 'Unable to restore default Keychain' unless system('security', 'default-keychain', '-d', 'user', '-s', state.fetch('default'))
  abort 'Unable to restore Keychain search list' unless system('security', 'list-keychains', '-d', 'user', '-s', *state.fetch('search'))
  File.delete(path)
end
