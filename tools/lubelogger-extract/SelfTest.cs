using LiteDB;

internal static class SelfTest
{
    internal static void Run()
    {
        if (OperatingSystem.IsWindows()) throw new PlatformNotSupportedException();
        var workspace = Extractor.NewWorkspace();
        try
        {
            var input = Path.Combine(workspace, "source.db");
            var output = Path.Combine(workspace, "export.json");
            var instant = new DateTime(2026, 3, 8, 5, 30, 0, DateTimeKind.Utc).AddMilliseconds(123);
            var original = new BsonDocument
            {
                ["_id"] = 7,
                ["decimal"] = 12345678901234567890.123456789m,
                ["date"] = instant,
                ["unknown"] = new BsonDocument { ["nested"] = new BsonArray { "keep exactly\n", 1234567890123456789L, true, BsonValue.Null } },
                ["reference"] = new BsonDocument { ["$ref"] = "vehicles", ["$id"] = 1 },
                ["binary"] = new byte[] { 0, 1, 255 }
            };
            using (var database = new LiteDatabase(input))
            {
                database.CheckpointSize = 0;
                database.GetCollection("records").Insert(original);
                database.GetCollection("vehicles").Insert(new BsonDocument { ["_id"] = 1, ["label"] = "Synthetic vehicle" });
            }
            var wal = Extractor.WalPath(input);
            Assert(File.Exists(wal) && new FileInfo(wal).Length > 0, "fixture must require WAL recovery");
            var dbHash = Extractor.Hash(input);
            var walHash = Extractor.Hash(wal);
            var counts = Extractor.Export(input, output);
            Assert(counts["records"] == 1 && counts["vehicles"] == 1, "recovered counts");
            var json = File.ReadAllText(output);
            var exported = LiteDB.JsonSerializer.Deserialize(json).AsDocument;
            var actual = exported["collections"]["records"][0].AsDocument;
            Assert(actual["decimal"].IsDecimal && actual["decimal"].AsDecimal == original["decimal"].AsDecimal, "exact decimal");
            Assert(actual["date"].IsDateTime && actual["date"].AsDateTime.ToUniversalTime() == instant, "UTC millisecond date");
            Assert(actual["_id"].IsInt32 && actual["_id"].AsInt32 == 7, "typed ID");
            Assert(actual["unknown"]["nested"][1].IsInt64, "nested long type");
            Assert(actual["unknown"]["nested"][0].AsString == "keep exactly\n", "unknown string");
            Assert(actual["reference"]["$id"].AsInt32 == 1, "relationship");
            Assert(actual["binary"].AsBinary.SequenceEqual(new byte[] { 0, 1, 255 }), "binary");
            Assert(json.Contains("\"$numberDecimal\"") && json.Contains("\"$date\""), "extended JSON markers");
            Assert(Extractor.Hash(input).SequenceEqual(dbHash) && Extractor.Hash(wal).SequenceEqual(walHash), "source unchanged");
            Assert(File.GetUnixFileMode(output) == Extractor.PrivateFile, "private output permissions");
            Reject(() => Extractor.Export(input, input), "database collision");
            Reject(() => Extractor.Export(input, wal), "WAL collision");
            Reject(() => Extractor.Export(input, output), "existing output");
            Assert(File.ReadAllText(output) == json, "existing output unchanged");
            Reject(() => Extractor.Export(Path.Combine(workspace, "missing.db"), Path.Combine(workspace, "missing.json")), "missing source");
            var corrupt = Path.Combine(workspace, "corrupt.db");
            File.WriteAllText(corrupt, "invalid database");
            var corruptOutput = Path.Combine(workspace, "corrupt.json");
            Reject(() => Extractor.Export(corrupt, corruptOutput), "corrupt database");
            Assert(!File.Exists(corruptOutput), "failure must not publish output");
            File.WriteAllBytes(corrupt, new byte[8192]);
            Reject(() => Extractor.Export(corrupt, corruptOutput), "invalid database header");
            Assert(!File.Exists(corruptOutput), "invalid header must not publish output");
            var linked = Path.Combine(workspace, "linked.db");
            File.CreateSymbolicLink(linked, input);
            Reject(() => Extractor.Export(linked, Path.Combine(workspace, "linked.json")), "symbolic link source");
            Assert(!Directory.EnumerateFiles(workspace, "*.partial").Any(), "failed export cleanup");
        }
        finally
        {
            Directory.Delete(workspace, recursive: true);
        }
    }

    private static void Assert(bool condition, string reason)
    {
        if (!condition) throw new InvalidOperationException("self-test failed: " + reason);
    }

    private static void Reject(Action action, string reason)
    {
        var failed = false;
        try { action(); }
        catch (InputException) { failed = true; }
        catch (IOException) { failed = true; }
        catch (LiteException) { failed = true; }
        Assert(failed, reason);
    }
}
