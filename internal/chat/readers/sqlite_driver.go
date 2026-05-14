package readers

// Register the pure-Go SQLite driver so OpenCode and Kiro CLI discovery work
// without CGO or any system library. modernc.org/sqlite registers itself as
// "sqlite" on init; the default driver name used by SQLiteReader, OpenCodeReader,
// and KiroReader is updated to match.
import _ "modernc.org/sqlite"
