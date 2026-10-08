using System.Security.Cryptography;
using System.Text;
using LiteDB;

return Extractor.Run(args);

internal static class Extractor
{
    internal const UnixFileMode PrivateFile = UnixFileMode.UserRead | UnixFileMode.UserWrite;
    private const UnixFileMode PrivateDirectory = PrivateFile | UnixFileMode.UserExecute;

    internal static int Run(string[] args)
    {
        if (args.SequenceEqual(new[] { "--help" }))
        {
            Console.WriteLine("lubelogger-extract --input <copied-database.db> --output <new-export.json>\nlubelogger-extract --self-test\nReads a consistent offline snapshot plus its matching -log.db. Never use the live database. Output includes every collection and must stay private.");
            return 0;
        }
        if (OperatingSystem.IsWindows())
        {
            Console.Error.WriteLine("error: this extractor requires Unix file permissions (macOS or Linux)");
            return 1;
        }
        try
        {
            if (args.SequenceEqual(new[] { "--self-test" }))
            {
                SelfTest.Run();
                Console.WriteLine("self-test passed: WAL recovery, BSON types, source preservation, permissions, and failure paths");
                return 0;
            }
            if (args.Length != 4 || args[0] != "--input" || args[2] != "--output")
                throw new InputException("expected --input <copied-database.db> --output <new-export.json>");
            var counts = Export(args[1], args[3]);
            Console.WriteLine(System.Text.Json.JsonSerializer.Serialize(new { collections = counts }));
            return 0;
        }
        catch (InputException error)
        {
            Console.Error.WriteLine($"error: {error.Message}");
        }
        catch (Exception error)
        {
            if (args.SequenceEqual(new[] { "--self-test" }))
            {
                Console.Error.WriteLine($"self-test failed: {error}");
                return 1;
            }
            // Database errors can include document values, so only expose a bounded category.
            var category = error switch
            {
                UnauthorizedAccessException => "permission denied",
                IOException => "file operation failed",
                LiteException => "database read failed",
                _ => "extraction failed"
            };
            Console.Error.WriteLine($"error: {category}; no successful export was published");
        }
        return 1;
    }

    internal static SortedDictionary<string, long> Export(string input, string output)
    {
        if (OperatingSystem.IsWindows()) throw new PlatformNotSupportedException();
        input = Path.GetFullPath(input);
        output = Path.GetFullPath(output);
        var wal = WalPath(input);
        if (!File.Exists(input) || new FileInfo(input).LinkTarget != null)
            throw new InputException("input must be an existing regular snapshot file, not a symbolic link");
        if (input == output || wal == output || Path.Exists(output) || new FileInfo(output).LinkTarget != null)
            throw new InputException("output must be a new file distinct from the source database and WAL");
        var parent = Path.GetDirectoryName(output)!;
        if (!Directory.Exists(parent)) throw new InputException("output directory must already exist");

        var workspace = NewWorkspace();
        var partial = Path.Combine(parent, $".lubelogger-export-{Guid.NewGuid():N}.partial");
        try
        {
            var clone = Path.Combine(workspace, "snapshot.db");
            CloneSnapshot(input, wal, clone);
            // LiteDB initializes undersized files as new databases instead of rejecting them.
            if (new FileInfo(clone).Length < 8192)
                throw new InputException("snapshot is too small to contain a LiteDB database header");
            var counts = new SortedDictionary<string, long>(StringComparer.Ordinal);
            using (var stream = NewPrivateFile(partial))
            using (var writer = new StreamWriter(stream, new UTF8Encoding(false), leaveOpen: true))
            {
                // WAL recovery and the UTC pragma may write, exclusively inside this private clone.
                using var database = new LiteDatabase(new ConnectionString { Filename = clone });
                database.UtcDate = true;
                writer.Write("{\"formatVersion\":1,\"collections\":{");
                var firstCollection = true;
                foreach (var name in database.GetCollectionNames().OrderBy(name => name, StringComparer.Ordinal))
                {
                    if (!firstCollection) writer.Write(',');
                    firstCollection = false;
                    writer.Write(System.Text.Json.JsonSerializer.Serialize(name));
                    writer.Write(":[");
                    long count = 0;
                    foreach (var document in database.GetCollection(name).Find(Query.All(Query.Ascending)))
                    {
                        if (count++ != 0) writer.Write(',');
                        LiteDB.JsonSerializer.Serialize(document, writer);
                    }
                    writer.Write(']');
                    counts[name] = count;
                }
                writer.Write("}}\n");
                writer.Flush();
                stream.Flush(flushToDisk: true);
            }
            Directory.Delete(workspace, recursive: true);
            File.Move(partial, output, overwrite: false);
            return counts;
        }
        finally
        {
            if (File.Exists(partial)) File.Delete(partial);
            if (Directory.Exists(workspace)) Directory.Delete(workspace, recursive: true);
        }
    }

    internal static string WalPath(string input) => Path.Combine(
        Path.GetDirectoryName(input)!, Path.GetFileNameWithoutExtension(input) + "-log" + Path.GetExtension(input));

    internal static string NewWorkspace()
    {
        if (OperatingSystem.IsWindows()) throw new PlatformNotSupportedException();
        var directory = Path.Combine(Path.GetTempPath(), "pitpilot-lubelogger-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(directory, PrivateDirectory);
        return directory;
    }

    internal static FileStream NewPrivateFile(string file)
    {
        if (OperatingSystem.IsWindows()) throw new PlatformNotSupportedException();
        return new FileStream(file, new FileStreamOptions
        {
            Mode = FileMode.CreateNew, Access = FileAccess.Write, Share = FileShare.None,
            UnixCreateMode = PrivateFile
        });
    }

    internal static byte[] Hash(string file)
    {
        using var stream = File.OpenRead(file);
        return SHA256.HashData(stream);
    }

    private static void CloneSnapshot(string input, string wal, string clone)
    {
        var hasWal = File.Exists(wal);
        if (new FileInfo(wal).LinkTarget != null) throw new InputException("snapshot WAL must not be a symbolic link");
        var before = Hash(input);
        var walBefore = hasWal ? Hash(wal) : null;
        CopyPrivate(input, clone);
        if (hasWal) CopyPrivate(wal, WalPath(clone));
        if (File.Exists(wal) != hasWal || !before.SequenceEqual(Hash(input)) || !before.SequenceEqual(Hash(clone)) ||
            (hasWal && (!walBefore!.SequenceEqual(Hash(wal)) || !walBefore!.SequenceEqual(Hash(WalPath(clone))))))
            throw new IOException("snapshot changed during copying");
    }

    private static void CopyPrivate(string source, string destination)
    {
        using var input = File.OpenRead(source);
        using var output = NewPrivateFile(destination);
        input.CopyTo(output);
    }
}

internal sealed class InputException(string message) : Exception(message);
