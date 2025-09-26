package db

import (
	"path/filepath"

	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"
)

func OpenBadger(dir string) (*badger.DB, error) {
	opts := badger.DefaultOptions(filepath.Clean(dir))
	opts = opts.WithNumVersionsToKeep(1)
	opts = opts.WithLogger(nil)
	opts = opts.WithCompression(options.ZSTD)
	opts = opts.WithIndexCacheSize(64 << 20).WithBlockCacheSize(128 << 20)
	return badger.Open(opts)
}
