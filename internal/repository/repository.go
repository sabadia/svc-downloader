package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/sabadia/svc-downloader/internal/db"
	"github.com/sabadia/svc-downloader/internal/errs"
	"github.com/sabadia/svc-downloader/internal/models"
)

// BadgerRepository implements models.Repository backed by BadgerDB.
type BadgerRepository struct {
	db *badger.DB
}

// NewBadgerRepository opens/creates a BadgerDB at dir with recommended options.
func NewBadgerRepository(dir string) (*BadgerRepository, error) {
	badgerDb, err := db.OpenBadger(dir)
	if err != nil {
		return nil, err
	}
	return &BadgerRepository{db: badgerDb}, nil
}

func (r *BadgerRepository) Close() error { return r.db.Close() }

// ---- Keys and indexing ----

var (
	pDownload            = []byte("d|")
	pDownloadByCreatedAt = []byte("dca|")
	pDownloadByStatus    = []byte("dst|") // dst|<status>|<createdKey>|<id>
	pDownloadByQueue     = []byte("dq|")  // dq|<queueID>|<createdKey>|<id>
	pDownloadByTag       = []byte("dt|")  // dt|<tag>|<createdKey>|<id>
	pQueue               = []byte("q|")   // q|<id>
	pQueueStats          = []byte("qs|")  // qs|<queueID>
	pSegment             = []byte("s|")   // s|<downloadID>|<index>
)

func kDownload(id string) []byte { return append(append([]byte{}, pDownload...), []byte(id)...) }

func kDownloadByCreatedAt(t time.Time, id string) []byte {
	return bytes.Join([][]byte{pDownloadByCreatedAt, []byte(timeToKey(t)), []byte(id)}, []byte("|"))
}

func kDownloadByStatus(status models.DownloadStatus, t time.Time, id string) []byte {
	return bytes.Join([][]byte{pDownloadByStatus, []byte(status), []byte(timeToKey(t)), []byte(id)}, []byte("|"))
}

func kDownloadByQueue(queueID string, t time.Time, id string) []byte {
	return bytes.Join([][]byte{pDownloadByQueue, []byte(queueID), []byte(timeToKey(t)), []byte(id)}, []byte("|"))
}

func kDownloadByTag(tag string, t time.Time, id string) []byte {
	return bytes.Join([][]byte{pDownloadByTag, []byte(tag), []byte(timeToKey(t)), []byte(id)}, []byte("|"))
}

func kQueue(id string) []byte      { return append(append([]byte{}, pQueue...), []byte(id)...) }
func kQueueStats(id string) []byte { return append(append([]byte{}, pQueueStats...), []byte(id)...) }

func kSegment(downloadID string, index int) []byte {
	return bytes.Join([][]byte{pSegment, []byte(downloadID), []byte(fmt.Sprintf("%09d", index))}, []byte("|"))
}

func timeToKey(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return fmt.Sprintf("%020d", t.UTC().UnixNano())
}

// indexDownload creates secondary index entries for a download
func indexDownload(txn *badger.Txn, d *models.Download) error {
	createdKey := d.CreatedAt
	if err := txn.Set(kDownloadByCreatedAt(createdKey, d.ID), nil); err != nil {
		return err
	}
	if d.Status != "" {
		if err := txn.Set(kDownloadByStatus(d.Status, createdKey, d.ID), nil); err != nil {
			return err
		}
	}
	if d.QueueID != "" {
		if err := txn.Set(kDownloadByQueue(d.QueueID, createdKey, d.ID), nil); err != nil {
			return err
		}
	}
	for _, tag := range d.Tags {
		if tag == "" {
			continue
		}
		if err := txn.Set(kDownloadByTag(strings.ToLower(tag), createdKey, d.ID), nil); err != nil {
			return err
		}
	}
	return nil
}

// indexDownloadBatch creates index entries using a WriteBatch (for bulk writes)
func indexDownloadBatch(wb *badger.WriteBatch, d *models.Download) error {
	createdKey := d.CreatedAt
	if err := wb.Set(kDownloadByCreatedAt(createdKey, d.ID), nil); err != nil {
		return err
	}
	if d.Status != "" {
		if err := wb.Set(kDownloadByStatus(d.Status, createdKey, d.ID), nil); err != nil {
			return err
		}
	}
	if d.QueueID != "" {
		if err := wb.Set(kDownloadByQueue(d.QueueID, createdKey, d.ID), nil); err != nil {
			return err
		}
	}
	for _, tag := range d.Tags {
		if tag == "" {
			continue
		}
		if err := wb.Set(kDownloadByTag(strings.ToLower(tag), createdKey, d.ID), nil); err != nil {
			return err
		}
	}
	return nil
}

// unindexDownload removes secondary index entries for a download
func unindexDownload(txn *badger.Txn, d *models.Download) {
	if d == nil || d.CreatedAt.IsZero() {
		return
	}
	createdKey := d.CreatedAt
	_ = txn.Delete(kDownloadByCreatedAt(createdKey, d.ID))
	if d.Status != "" {
		_ = txn.Delete(kDownloadByStatus(d.Status, createdKey, d.ID))
	}
	if d.QueueID != "" {
		_ = txn.Delete(kDownloadByQueue(d.QueueID, createdKey, d.ID))
	}
	for _, tag := range d.Tags {
		if tag != "" {
			_ = txn.Delete(kDownloadByTag(strings.ToLower(tag), createdKey, d.ID))
		}
	}
}

// listDownloadsTxn contains the iterator and filtering logic shared by repo and txRepo
func listDownloadsTxn(txn *badger.Txn, ctx context.Context, options models.ListDownloadsOptions, limit, offset int) ([]models.Download, error) {
	iterOpt := badger.DefaultIteratorOptions
	iterOpt.PrefetchValues = false
	iterOpt.PrefetchSize = 64
	var prefix []byte
	if len(options.QueueIDs) == 1 {
		prefix = bytes.Join([][]byte{pDownloadByQueue, []byte(options.QueueIDs[0]), nil}, []byte("|"))
		iterOpt.Prefix = prefix[:len(prefix)-1]
	} else if len(options.Statuses) == 1 {
		prefix = bytes.Join([][]byte{pDownloadByStatus, []byte(options.Statuses[0]), nil}, []byte("|"))
		iterOpt.Prefix = prefix[:len(prefix)-1]
	} else {
		iterOpt.Prefix = pDownloadByCreatedAt
	}
	iterOpt.Reverse = options.OrderDesc
	it := txn.NewIterator(iterOpt)
	defer it.Close()

	discard := offset
	collect := limit
	var result []models.Download
	fetch := func(id string) (*models.Download, error) {
		it2, err := txn.Get(kDownload(id))
		if err != nil {
			return nil, err
		}
		var d models.Download
		if err := it2.Value(func(b []byte) error { return json.Unmarshal(b, &d) }); err != nil {
			return nil, err
		}
		// In-memory filters
		if len(options.QueueIDs) > 0 {
			ok := false
			for _, q := range options.QueueIDs {
				if d.QueueID == q {
					ok = true
					break
				}
			}
			if !ok {
				return nil, nil
			}
		}
		if len(options.Statuses) > 0 {
			ok := false
			for _, s := range options.Statuses {
				if d.Status == s {
					ok = true
					break
				}
			}
			if !ok {
				return nil, nil
			}
		}
		if len(options.Tags) > 0 {
			ok := false
			for _, t := range options.Tags {
				for _, dt := range d.Tags {
					if strings.EqualFold(dt, t) {
						ok = true
						break
					}
				}
				if ok {
					break
				}
			}
			if !ok {
				return nil, nil
			}
		}
		if options.Search != "" {
			q := strings.ToLower(options.Search)
			candidate := d.URL
			if d.File != nil {
				candidate += "|" + d.File.Filename
			}
			if !strings.Contains(strings.ToLower(candidate), q) {
				return nil, nil
			}
		}
		if options.CreatedAfter != nil && d.CreatedAt.Before(*options.CreatedAfter) {
			return nil, nil
		}
		if options.CreatedBefore != nil && d.CreatedAt.After(*options.CreatedBefore) {
			return nil, nil
		}
		return &d, nil
	}

	seekPrefix := iterOpt.Prefix
	if iterOpt.Reverse {
		end := append(append([]byte{}, seekPrefix...), 0xFF)
		it.Seek(end)
	} else {
		it.Rewind()
	}
	for ; it.ValidForPrefix(seekPrefix); it.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := it.Item().KeyCopy(nil)
		parts := bytes.Split(key, []byte("|"))
		if len(parts) == 0 {
			continue
		}
		id := string(parts[len(parts)-1])
		d, err := fetch(id)
		if err == badger.ErrKeyNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		if d == nil {
			continue
		}
		if discard > 0 {
			discard--
			continue
		}
		result = append(result, *d)
		if collect > 0 {
			collect--
		}
		if collect == 0 {
			break
		}
	}

	// Optional post-ordering by CreatedAt if OrderBy specified
	if options.OrderBy == "created_at" || options.OrderBy == "" {
		sort.Slice(result, func(i, j int) bool {
			if options.OrderDesc {
				return result[i].CreatedAt.After(result[j].CreatedAt)
			}
			return result[i].CreatedAt.Before(result[j].CreatedAt)
		})
	}
	return result, nil
}

// deleteByPrefix deletes all keys under a prefix using a single iterator
func deleteByPrefix(txn *badger.Txn, ctx context.Context, prefix []byte) error {
	it := txn.NewIterator(badger.IteratorOptions{Prefix: prefix})
	defer it.Close()
	for it.Rewind(); it.ValidForPrefix(prefix); it.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		k := it.Item().KeyCopy(nil)
		if err := txn.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

// ---- Public transactional API ----

func (r *BadgerRepository) RunInTx(ctx context.Context, fn func(ctx context.Context, tx models.Repository) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.db.Update(func(txn *badger.Txn) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fn(ctx, &txRepo{r: r, txn: txn})
	})
}

// Provide convenience helpers for non-nested operations
func (r *BadgerRepository) view(ctx context.Context, f func(*badger.Txn) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.db.View(func(txn *badger.Txn) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return f(txn)
	})
}

func (r *BadgerRepository) update(ctx context.Context, f func(*badger.Txn) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.db.Update(func(txn *badger.Txn) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return f(txn)
	})
}

// ---- txRepo gives a txn-scoped implementation ----

type txRepo struct {
	r   *BadgerRepository
	txn *badger.Txn
}

// ---- txRepo methods delegate to underlying helpers using the held txn ----

func (t *txRepo) RunInTx(ctx context.Context, fn func(ctx context.Context, tx models.Repository) error) error {
	return fn(ctx, t)
}

func (t *txRepo) CreateDownload(ctx context.Context, d *models.Download) error {
	return t.r.createDownloadTxn(ctx, t.txn, d)
}
func (t *txRepo) UpdateDownload(ctx context.Context, d *models.Download) error {
	return t.r.updateDownloadTxn(ctx, t.txn, d)
}
func (t *txRepo) GetDownload(ctx context.Context, id string) (*models.Download, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	item, err := t.txn.Get(kDownload(id))
	if err != nil {
		if err == badger.ErrKeyNotFound {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	var d models.Download
	if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &d) }); err != nil {
		return nil, err
	}
	return &d, nil
}
func (t *txRepo) ListDownloads(ctx context.Context, options models.ListDownloadsOptions, limit, offset int) ([]models.Download, error) {
	return listDownloadsTxn(t.txn, ctx, options, limit, offset)
}
func (t *txRepo) DeleteDownload(ctx context.Context, id string) error {
	return t.r.DeleteDownload(ctx, id)
}
func (t *txRepo) UpsertSegment(ctx context.Context, s *models.Segment) error {
	return t.r.UpsertSegment(ctx, s)
}
func (t *txRepo) ListSegments(ctx context.Context, downloadID string) ([]models.Segment, error) {
	return t.r.ListSegments(ctx, downloadID)
}
func (t *txRepo) UpdateSegment(ctx context.Context, s *models.Segment) error {
	return t.r.UpdateSegment(ctx, s)
}
func (t *txRepo) DeleteSegments(ctx context.Context, downloadID string) error {
	return t.r.DeleteSegments(ctx, downloadID)
}
func (t *txRepo) SaveQueue(ctx context.Context, q *models.Queue) error { return t.r.SaveQueue(ctx, q) }
func (t *txRepo) GetQueue(ctx context.Context, id string) (*models.Queue, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	item, err := t.txn.Get(kQueue(id))
	if err != nil {
		if err == badger.ErrKeyNotFound {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	var q models.Queue
	if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &q) }); err != nil {
		return nil, err
	}
	return &q, nil
}
func (t *txRepo) ListQueues(ctx context.Context) ([]models.Queue, error) { return t.r.ListQueues(ctx) }
func (t *txRepo) DeleteQueue(ctx context.Context, id string) error       { return t.r.DeleteQueue(ctx, id) }
func (t *txRepo) SaveQueueStats(ctx context.Context, stats models.QueueStats) error {
	return t.r.SaveQueueStats(ctx, stats)
}
func (t *txRepo) GetQueueStats(ctx context.Context, id string) (models.QueueStats, error) {
	if err := ctx.Err(); err != nil {
		return models.QueueStats{}, err
	}
	item, err := t.txn.Get(kQueueStats(id))
	if err != nil {
		if err == badger.ErrKeyNotFound {
			return models.QueueStats{}, errs.ErrNotFound
		}
		return models.QueueStats{}, err
	}
	var stats models.QueueStats
	if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &stats) }); err != nil {
		return models.QueueStats{}, err
	}
	return stats, nil
}
func (t *txRepo) BulkCreateDownloads(ctx context.Context, downloads []models.Download) error {
	return t.r.BulkCreateDownloads(ctx, downloads)
}
func (t *txRepo) BulkUpdateDownloads(ctx context.Context, downloads []models.Download) error {
	return t.r.BulkUpdateDownloads(ctx, downloads)
}
func (t *txRepo) BulkDeleteDownloads(ctx context.Context, ids []string) error {
	return t.r.BulkDeleteDownloads(ctx, ids)
}
func (t *txRepo) BulkDeleteDownloadsByQueueID(ctx context.Context, queueID string) error {
	return t.r.BulkDeleteDownloadsByQueueID(ctx, queueID)
}
func (t *txRepo) BulkReassignDownloadsQueue(ctx context.Context, fromQueueID, toQueueID string) error {
	return t.r.BulkReassignDownloadsQueue(ctx, fromQueueID, toQueueID)
}
func (t *txRepo) BulkSetPriorityByQueueID(ctx context.Context, queueID string, priority int) error {
	return t.r.BulkSetPriorityByQueueID(ctx, queueID, priority)
}
func (t *txRepo) BulkUpdateStatusByQueueID(ctx context.Context, queueID string, status models.DownloadStatus) error {
	return t.r.BulkUpdateStatusByQueueID(ctx, queueID, status)
}

// ---- Downloads ----

func (r *BadgerRepository) CreateDownload(ctx context.Context, d *models.Download) error {
	return r.update(ctx, func(txn *badger.Txn) error { return r.createDownloadTxn(ctx, txn, d) })
}

func (r *BadgerRepository) createDownloadTxn(ctx context.Context, txn *badger.Txn, d *models.Download) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.ID == "" {
		return errors.New("download id is required")
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	d.UpdatedAt = time.Now().UTC()
	val, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := txn.Set(kDownload(d.ID), val); err != nil {
		return err
	}
	return indexDownload(txn, d)
}

func (r *BadgerRepository) UpdateDownload(ctx context.Context, d *models.Download) error {
	return r.update(ctx, func(txn *badger.Txn) error { return r.updateDownloadTxn(ctx, txn, d) })
}

func (r *BadgerRepository) updateDownloadTxn(ctx context.Context, txn *badger.Txn, d *models.Download) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.ID == "" {
		return errors.New("download id is required")
	}
	item, err := txn.Get(kDownload(d.ID))
	if err != nil {
		return err
	}
	var old models.Download
	if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &old) }); err != nil {
		return err
	}

	// Remove old indexes
	unindexDownload(txn, &old)

	// Upsert new
	if d.CreatedAt.IsZero() {
		d.CreatedAt = old.CreatedAt
	}
	d.UpdatedAt = time.Now().UTC()
	val, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := txn.Set(kDownload(d.ID), val); err != nil {
		return err
	}
	return indexDownload(txn, d)
}

func (r *BadgerRepository) GetDownload(ctx context.Context, id string) (*models.Download, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out *models.Download
	err := r.view(ctx, func(txn *badger.Txn) error {
		item, err := txn.Get(kDownload(id))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return errs.ErrNotFound
			}
			return err
		}
		var d models.Download
		if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &d) }); err != nil {
			return err
		}
		out = &d
		return nil
	})
	return out, err
}

// BulkCreateDownloads inserts many downloads efficiently using WriteBatch.
// Note: This bypasses normal transactions for throughput; index entries are maintained.
func (r *BadgerRepository) BulkCreateDownloads(ctx context.Context, downloads []models.Download) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	wb := r.db.NewWriteBatch()
	defer wb.Cancel()
	wb.SetMaxPendingTxns(64)

	const flushEvery = 1000
	pending := 0

	for i := range downloads {
		if err := ctx.Err(); err != nil {
			return err
		}
		d := &downloads[i]
		if d.ID == "" {
			return errors.New("download id is required")
		}
		if d.CreatedAt.IsZero() {
			d.CreatedAt = time.Now().UTC()
		}
		d.UpdatedAt = time.Now().UTC()
		val, err := json.Marshal(d)
		if err != nil {
			return err
		}
		if err := wb.Set(kDownload(d.ID), val); err != nil {
			return err
		}
		if err := indexDownloadBatch(wb, d); err != nil {
			return err
		}
		pending++
		if pending%flushEvery == 0 {
			if err := wb.Flush(); err != nil {
				return err
			}
		}
	}
	return wb.Flush()
}

// BulkUpdateDownloads updates many downloads efficiently. It removes old indexes and writes new ones.
func (r *BadgerRepository) BulkUpdateDownloads(ctx context.Context, downloads []models.Download) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(downloads) == 0 {
		return nil
	}

	oldMap := make(map[string]models.Download, len(downloads))
	if err := r.view(ctx, func(txn *badger.Txn) error {
		for i := range downloads {
			if err := ctx.Err(); err != nil {
				return err
			}
			id := downloads[i].ID
			if id == "" {
				return errors.New("download id is required")
			}
			item, err := txn.Get(kDownload(id))
			if err != nil {
				return err
			}
			var d models.Download
			if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &d) }); err != nil {
				return err
			}
			oldMap[id] = d
		}
		return nil
	}); err != nil {
		return err
	}

	const chunkSize = 1000
	for i := 0; i < len(downloads); i += chunkSize {
		end := i + chunkSize
		if end > len(downloads) {
			end = len(downloads)
		}
		if err := r.db.Update(func(txn *badger.Txn) error {
			for j := i; j < end; j++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				old := oldMap[downloads[j].ID]
				unindexDownload(txn, &old)
			}
			return nil
		}); err != nil {
			return err
		}
	}

	wb := r.db.NewWriteBatch()
	defer wb.Cancel()
	wb.SetMaxPendingTxns(64)

	const flushEvery = 1000
	pending := 0
	for i := range downloads {
		if err := ctx.Err(); err != nil {
			return err
		}
		d := &downloads[i]
		old := oldMap[d.ID]
		if d.CreatedAt.IsZero() {
			d.CreatedAt = old.CreatedAt
		}
		d.UpdatedAt = time.Now().UTC()
		val, err := json.Marshal(d)
		if err != nil {
			return err
		}
		if err := wb.Set(kDownload(d.ID), val); err != nil {
			return err
		}
		if err := indexDownloadBatch(wb, d); err != nil {
			return err
		}
		pending++
		if pending%flushEvery == 0 {
			if err := wb.Flush(); err != nil {
				return err
			}
		}
	}
	return wb.Flush()
}

// BulkDeleteDownloads deletes many downloads and their indexes/segments efficiently.
func (r *BadgerRepository) BulkDeleteDownloads(ctx context.Context, ids []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	type rec struct{ d models.Download }
	toDelete := make([]rec, 0, len(ids))
	if err := r.view(ctx, func(txn *badger.Txn) error {
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			if id == "" {
				return errors.New("download id is required")
			}
			item, err := txn.Get(kDownload(id))
			if err != nil {
				return err
			}
			var d models.Download
			if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &d) }); err != nil {
				return err
			}
			toDelete = append(toDelete, rec{d: d})
		}
		return nil
	}); err != nil {
		return err
	}

	wb := r.db.NewWriteBatch()
	defer wb.Cancel()
	wb.SetMaxPendingTxns(64)

	const flushEvery = 1000
	pending := 0

	const chunkSize = 1000
	for i := 0; i < len(toDelete); i += chunkSize {
		end := i + chunkSize
		if end > len(toDelete) {
			end = len(toDelete)
		}
		if err := r.db.Update(func(txn *badger.Txn) error {
			for j := i; j < end; j++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				d := &toDelete[j].d
				unindexDownload(txn, d)
				sp := bytes.Join([][]byte{pSegment, []byte(d.ID)}, []byte("|"))
				if err := deleteByPrefix(txn, ctx, sp); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	for i := range toDelete {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := wb.Delete(kDownload(toDelete[i].d.ID)); err != nil {
			return err
		}
		pending++
		if pending%flushEvery == 0 {
			if err := wb.Flush(); err != nil {
				return err
			}
		}
	}
	return wb.Flush()
}

func (r *BadgerRepository) ListDownloads(ctx context.Context, options models.ListDownloadsOptions, limit, offset int) ([]models.Download, error) {
	var result []models.Download
	err := r.view(ctx, func(txn *badger.Txn) error {
		list, err := listDownloadsTxn(txn, ctx, options, limit, offset)
		if err != nil {
			return err
		}
		result = list
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *BadgerRepository) DeleteDownload(ctx context.Context, id string) error {
	return r.update(ctx, func(txn *badger.Txn) error {
		item, err := txn.Get(kDownload(id))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return errs.ErrNotFound
			}
			return err
		}
		var d models.Download
		if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &d) }); err != nil {
			return err
		}
		// delete primary
		if err := txn.Delete(kDownload(id)); err != nil {
			return err
		}
		// delete indexes
		unindexDownload(txn, &d)
		// delete segments by prefix
		sp := bytes.Join([][]byte{pSegment, []byte(d.ID)}, []byte("|"))
		return deleteByPrefix(txn, ctx, sp)
	})
}

// ---- Segments ----

func (r *BadgerRepository) UpsertSegment(ctx context.Context, s *models.Segment) error {
	return r.update(ctx, func(txn *badger.Txn) error {
		if s.DownloadID == "" {
			return errors.New("segment download_id is required")
		}
		val, err := json.Marshal(s)
		if err != nil {
			return err
		}
		return txn.Set(kSegment(s.DownloadID, s.Index), val)
	})
}

func (r *BadgerRepository) ListSegments(ctx context.Context, downloadID string) ([]models.Segment, error) {
	var out []models.Segment
	err := r.view(ctx, func(txn *badger.Txn) error {
		prefix := bytes.Join([][]byte{pSegment, []byte(downloadID)}, []byte("|"))
		it := txn.NewIterator(badger.IteratorOptions{Prefix: prefix})
		defer it.Close()
		for it.Rewind(); it.ValidForPrefix(prefix); it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			var s models.Segment
			if err := it.Item().Value(func(b []byte) error { return json.Unmarshal(b, &s) }); err != nil {
				return err
			}
			out = append(out, s)
		}
		return nil
	})
	return out, err
}

func (r *BadgerRepository) UpdateSegment(ctx context.Context, s *models.Segment) error {
	return r.UpsertSegment(ctx, s)
}

func (r *BadgerRepository) DeleteSegments(ctx context.Context, downloadID string) error {
	return r.update(ctx, func(txn *badger.Txn) error {
		prefix := bytes.Join([][]byte{pSegment, []byte(downloadID)}, []byte("|"))
		return deleteByPrefix(txn, ctx, prefix)
	})
}

// ---- Queues ----

func (r *BadgerRepository) SaveQueue(ctx context.Context, q *models.Queue) error {
	return r.update(ctx, func(txn *badger.Txn) error {
		if q.ID == "" {
			return errors.New("queue id is required")
		}
		if q.CreatedAt.IsZero() {
			q.CreatedAt = time.Now().UTC()
		}
		q.UpdatedAt = time.Now().UTC()
		val, err := json.Marshal(q)
		if err != nil {
			return err
		}
		return txn.Set(kQueue(q.ID), val)
	})
}

func (r *BadgerRepository) GetQueue(ctx context.Context, id string) (*models.Queue, error) {
	var out *models.Queue
	err := r.view(ctx, func(txn *badger.Txn) error {
		item, err := txn.Get(kQueue(id))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return errs.ErrNotFound
			}
			return err
		}
		var q models.Queue
		if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &q) }); err != nil {
			return err
		}
		out = &q
		return nil
	})
	return out, err
}

func (r *BadgerRepository) ListQueues(ctx context.Context) ([]models.Queue, error) {
	var out []models.Queue
	err := r.view(ctx, func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{Prefix: pQueue})
		defer it.Close()
		for it.Rewind(); it.ValidForPrefix(pQueue); it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			var q models.Queue
			if err := it.Item().Value(func(b []byte) error { return json.Unmarshal(b, &q) }); err != nil {
				return err
			}
			out = append(out, q)
		}
		return nil
	})
	return out, err
}

func (r *BadgerRepository) DeleteQueue(ctx context.Context, id string) error {
	return r.update(ctx, func(txn *badger.Txn) error {
		if err := txn.Delete(kQueue(id)); err != nil {
			return err
		}
		_ = txn.Delete(kQueueStats(id))
		return nil
	})
}

// ---- Queue Stats ----

func (r *BadgerRepository) SaveQueueStats(ctx context.Context, stats models.QueueStats) error {
	return r.update(ctx, func(txn *badger.Txn) error {
		val, err := json.Marshal(stats)
		if err != nil {
			return err
		}
		return txn.Set(kQueueStats(stats.QueueID), val)
	})
}

func (r *BadgerRepository) GetQueueStats(ctx context.Context, id string) (models.QueueStats, error) {
	var out models.QueueStats
	err := r.view(ctx, func(txn *badger.Txn) error {
		item, err := txn.Get(kQueueStats(id))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return errs.ErrNotFound
			}
			return err
		}
		return item.Value(func(b []byte) error { return json.Unmarshal(b, &out) })
	})
	return out, err
}
func (r *BadgerRepository) BulkDeleteDownloadsByQueueID(ctx context.Context, queueID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if queueID == "" {
		return errors.New("queue id is required")
	}
	var ids []string
	if err := r.view(ctx, func(txn *badger.Txn) error {
		prefix := bytes.Join([][]byte{pDownloadByQueue, []byte(queueID)}, []byte("|"))
		it := txn.NewIterator(badger.IteratorOptions{Prefix: prefix})
		defer it.Close()
		for it.Rewind(); it.ValidForPrefix(prefix); it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := it.Item().KeyCopy(nil)
			parts := bytes.Split(key, []byte("|"))
			if len(parts) == 0 {
				continue
			}
			ids = append(ids, string(parts[len(parts)-1]))
		}
		return nil
	}); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return r.BulkDeleteDownloads(ctx, ids)
}

func (r *BadgerRepository) BulkReassignDownloadsQueue(ctx context.Context, fromQueueID, toQueueID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fromQueueID == "" || toQueueID == "" {
		return errors.New("from and to queue ids are required")
	}
	if fromQueueID == toQueueID {
		return nil
	}
	var downloads []models.Download
	if err := r.view(ctx, func(txn *badger.Txn) error {
		prefix := bytes.Join([][]byte{pDownloadByQueue, []byte(fromQueueID)}, []byte("|"))
		it := txn.NewIterator(badger.IteratorOptions{Prefix: prefix})
		defer it.Close()
		for it.Rewind(); it.ValidForPrefix(prefix); it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := it.Item().KeyCopy(nil)
			parts := bytes.Split(key, []byte("|"))
			if len(parts) == 0 {
				continue
			}
			id := string(parts[len(parts)-1])
			item, err := txn.Get(kDownload(id))
			if err != nil {
				return err
			}
			var d models.Download
			if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &d) }); err != nil {
				return err
			}
			d.QueueID = toQueueID
			downloads = append(downloads, d)
		}
		return nil
	}); err != nil {
		return err
	}
	if len(downloads) == 0 {
		return nil
	}
	return r.BulkUpdateDownloads(ctx, downloads)
}

func (r *BadgerRepository) BulkSetPriorityByQueueID(ctx context.Context, queueID string, priority int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if queueID == "" {
		return errors.New("queue id is required")
	}
	var downloads []models.Download
	if err := r.view(ctx, func(txn *badger.Txn) error {
		prefix := bytes.Join([][]byte{pDownloadByQueue, []byte(queueID)}, []byte("|"))
		it := txn.NewIterator(badger.IteratorOptions{Prefix: prefix})
		defer it.Close()
		for it.Rewind(); it.ValidForPrefix(prefix); it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := it.Item().KeyCopy(nil)
			parts := bytes.Split(key, []byte("|"))
			if len(parts) == 0 {
				continue
			}
			id := string(parts[len(parts)-1])
			item, err := txn.Get(kDownload(id))
			if err != nil {
				return err
			}
			var d models.Download
			if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &d) }); err != nil {
				return err
			}
			d.Priority = priority
			downloads = append(downloads, d)
		}
		return nil
	}); err != nil {
		return err
	}
	if len(downloads) == 0 {
		return nil
	}
	return r.BulkUpdateDownloads(ctx, downloads)
}

func (r *BadgerRepository) BulkUpdateStatusByQueueID(ctx context.Context, queueID string, status models.DownloadStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if queueID == "" {
		return errors.New("queue id is required")
	}
	var downloads []models.Download
	if err := r.view(ctx, func(txn *badger.Txn) error {
		prefix := bytes.Join([][]byte{pDownloadByQueue, []byte(queueID)}, []byte("|"))
		it := txn.NewIterator(badger.IteratorOptions{Prefix: prefix})
		defer it.Close()
		for it.Rewind(); it.ValidForPrefix(prefix); it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := it.Item().KeyCopy(nil)
			parts := bytes.Split(key, []byte("|"))
			if len(parts) == 0 {
				continue
			}
			id := string(parts[len(parts)-1])
			item, err := txn.Get(kDownload(id))
			if err != nil {
				return err
			}
			var d models.Download
			if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &d) }); err != nil {
				return err
			}
			d.Status = status
			downloads = append(downloads, d)
		}
		return nil
	}); err != nil {
		return err
	}
	if len(downloads) == 0 {
		return nil
	}
	return r.BulkUpdateDownloads(ctx, downloads)
}

func (t *txRepo) CountDownloads(ctx context.Context, options models.ListDownloadsOptions) (int, error) {
	return t.r.CountDownloads(ctx, options)
}

func (r *BadgerRepository) CountDownloads(ctx context.Context, options models.ListDownloadsOptions) (int, error) {
	count := 0
	err := r.view(ctx, func(txn *badger.Txn) error {
		iterOpt := badger.DefaultIteratorOptions
		iterOpt.PrefetchValues = false
		var prefix []byte
		if len(options.QueueIDs) == 1 {
			prefix = bytes.Join([][]byte{pDownloadByQueue, []byte(options.QueueIDs[0]), nil}, []byte("|"))
			iterOpt.Prefix = prefix[:len(prefix)-1]
		} else if len(options.Statuses) == 1 {
			prefix = bytes.Join([][]byte{pDownloadByStatus, []byte(options.Statuses[0]), nil}, []byte("|"))
			iterOpt.Prefix = prefix[:len(prefix)-1]
		} else {
			iterOpt.Prefix = pDownloadByCreatedAt
		}
		it := txn.NewIterator(iterOpt)
		defer it.Close()
		for it.Rewind(); it.ValidForPrefix(iterOpt.Prefix); it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := it.Item().KeyCopy(nil)
			parts := bytes.Split(key, []byte("|"))
			if len(parts) == 0 {
				continue
			}
			id := string(parts[len(parts)-1])
			item, err := txn.Get(kDownload(id))
			if err != nil {
				continue
			}
			var d models.Download
			if err := item.Value(func(b []byte) error { return json.Unmarshal(b, &d) }); err != nil {
				continue
			}
			ok := true
			if len(options.QueueIDs) > 0 {
				ok = false
				for _, q := range options.QueueIDs {
					if d.QueueID == q {
						ok = true
						break
					}
				}
			}
			if ok && len(options.Statuses) > 0 {
				sok := false
				for _, s := range options.Statuses {
					if d.Status == s {
						sok = true
						break
					}
				}
				ok = sok
			}
			if ok && len(options.Tags) > 0 {
				tok := false
				for _, t := range options.Tags {
					for _, dt := range d.Tags {
						if strings.EqualFold(dt, t) {
							tok = true
							break
						}
					}
					if tok {
						break
					}
				}
				ok = tok
			}
			if ok && options.Search != "" {
				q := strings.ToLower(options.Search)
				candidate := d.URL
				if d.File != nil {
					candidate += "|" + d.File.Filename
				}
				ok = strings.Contains(strings.ToLower(candidate), q)
			}
			if ok && options.CreatedAfter != nil && d.CreatedAt.Before(*options.CreatedAfter) {
				ok = false
			}
			if ok && options.CreatedBefore != nil && d.CreatedAt.After(*options.CreatedBefore) {
				ok = false
			}
			if ok {
				count++
			}
		}
		return nil
	})
	return count, err
}
