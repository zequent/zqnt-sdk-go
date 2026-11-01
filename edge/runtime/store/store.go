// Package store is the edge runtime's durable outbox: one SQLite database (WAL, pure Go) that
// holds the node's unacknowledged envelopes in priority lanes, numbered (epoch, seq), and what the
// node has received of the platform's downlink.
//
// Appends are committed in batches to spare SD cards; lanes marked Sync commit at once. Only
// committed items are handed out by Take, so a sequence number the platform has seen is never
// reused, not even after a crash.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	_ "modernc.org/sqlite"
)

// Lane is the priority class of an item; lower values drain first.
type Lane = edgelinkv3.Lane

// Overflow is what a lane does when it exceeds its quota.
type Overflow int

const (
	// DropOldest evicts the lowest rank first and, within a rank, the oldest item.
	DropOldest Overflow = iota
	// Thin keeps every other item of the oldest half, round after round, until the lane fits.
	Thin
)

// LanePolicy is the quota and behaviour of one lane. Zero limits are unlimited.
type LanePolicy struct {
	MaxItems int
	MaxBytes int64
	// LatestPerKey replaces a queued item with the same key instead of adding another.
	LatestPerKey bool
	Overflow     Overflow
	// Sync commits every append at once instead of with the next batch.
	Sync bool
}

// DefaultLanes are the quotas of the transport plan (§5.6).
func DefaultLanes() map[Lane]LanePolicy {
	return map[Lane]LanePolicy{
		edgelinkv3.Lane_LANE_ALARM:         {MaxItems: 2000, Sync: true},
		edgelinkv3.Lane_LANE_COMMAND_REPLY: {MaxItems: 2000, Sync: true},
		edgelinkv3.Lane_LANE_STATUS:        {MaxItems: 500, LatestPerKey: true},
		edgelinkv3.Lane_LANE_TELEMETRY:     {MaxBytes: 50 << 20, Overflow: Thin},
		edgelinkv3.Lane_LANE_BULK:          {MaxBytes: 100 << 20},
	}
}

// Options configure a store; zero values take the defaults.
type Options struct {
	// Lanes defaults to DefaultLanes. Appends to a lane missing here are refused.
	Lanes map[Lane]LanePolicy
	// FlushInterval is the longest an append waits for its batch commit (200 ms).
	FlushInterval time.Duration
	// FlushItems commits as soon as this many appends are waiting (50).
	FlushItems int
	// NewEpoch numbers a new store; it defaults to the wall clock in seconds.
	NewEpoch func() uint32
	Now      func() time.Time
}

// Item is one queued envelope.
type Item struct {
	Seq  uint32
	Lane Lane
	// Key identifies what the item reports for LatestPerKey lanes, e.g. "capabilities/DOCK-1".
	Key string
	// Rank orders eviction in DropOldest lanes: lower ranks go first.
	Rank          int
	DeviceTime    time.Time
	ClockUnsynced bool
	Payload       []byte
}

// LaneBacklog is what one lane holds.
type LaneBacklog struct {
	Lane    Lane
	Items   int
	Bytes   int64
	Oldest  time.Time
	Dropped uint64
}

// ErrClosed is returned by calls on a closed store.
var ErrClosed = errors.New("store: closed")

type laneStat struct {
	items   int
	bytes   int64
	dropped uint64
}

type downlinkState struct {
	epoch uint32
	cum   uint32
	above map[uint32]struct{}
}

// Store is safe for concurrent use.
type Store struct {
	db   *sql.DB
	opts Options

	writeMu sync.Mutex

	mu           sync.Mutex
	closed       bool
	epoch        uint32
	nextSeq      uint32
	pending      []Item
	waiters      []chan error
	ackedThrough uint32
	acked        map[uint32]struct{}
	delThrough   uint32
	delSeqs      []uint32
	inflight     map[uint32]time.Time
	lanes        map[Lane]*laneStat
	downlink     downlinkState
	lastErr      error

	flushReq chan struct{}
	ready    chan struct{}
	stop     chan struct{}
	done     chan struct{}
}

const schema = `
CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS items (
	seq INTEGER PRIMARY KEY,
	lane INTEGER NOT NULL,
	key TEXT NOT NULL,
	rank INTEGER NOT NULL,
	device_time INTEGER NOT NULL,
	unsynced INTEGER NOT NULL,
	size INTEGER NOT NULL,
	payload BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS items_lane ON items (lane, seq);
CREATE INDEX IF NOT EXISTS items_key ON items (lane, key) WHERE key <> '';
CREATE TABLE IF NOT EXISTS downlink (seq INTEGER PRIMARY KEY);
`

// Open opens or creates the store at path.
func Open(path string, opts Options) (*Store, error) {
	if opts.Lanes == nil {
		opts.Lanes = DefaultLanes()
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 200 * time.Millisecond
	}
	if opts.FlushItems <= 0 {
		opts.FlushItems = 50
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NewEpoch == nil {
		now := opts.Now
		opts.NewEpoch = func() uint32 { return uint32(max(now().Unix(), 1)) }
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{
		db:       db,
		opts:     opts,
		acked:    map[uint32]struct{}{},
		inflight: map[uint32]time.Time{},
		lanes:    map[Lane]*laneStat{},
		downlink: downlinkState{above: map[uint32]struct{}{}},
		flushReq: make(chan struct{}, 1),
		ready:    make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if err := s.load(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	go s.run()
	return s, nil
}

func (s *Store) load() error {
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	meta, err := s.readMeta(ctx)
	if err != nil {
		return err
	}
	epoch, ok := meta["epoch"]
	if !ok {
		epoch = int64(s.opts.NewEpoch())
		if _, err := s.db.ExecContext(ctx, `INSERT INTO meta (k, v) VALUES ('epoch', ?)`, epoch); err != nil {
			return err
		}
	}
	s.epoch = uint32(epoch)
	var maxSeq sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM items`).Scan(&maxSeq); err != nil {
		return err
	}
	s.nextSeq = uint32(max(meta["seq"], maxSeq.Int64)) + 1
	rows, err := s.db.QueryContext(ctx, `SELECT lane, COUNT(*), SUM(size) FROM items GROUP BY lane`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var lane Lane
		var st laneStat
		if err := rows.Scan(&lane, &st.items, &st.bytes); err != nil {
			return err
		}
		s.lanes[lane] = &st
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.downlink.epoch = uint32(meta["dl_epoch"])
	s.downlink.cum = uint32(meta["dl_cum"])
	dl, err := s.db.QueryContext(ctx, `SELECT seq FROM downlink`)
	if err != nil {
		return err
	}
	defer func() { _ = dl.Close() }()
	for dl.Next() {
		var seq uint32
		if err := dl.Scan(&seq); err != nil {
			return err
		}
		s.downlink.above[seq] = struct{}{}
	}
	return dl.Err()
}

func (s *Store) readMeta(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT k, v FROM meta`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	meta := map[string]int64{}
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		meta[k] = v
	}
	return meta, rows.Err()
}

// Close commits what is waiting and closes the database.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	close(s.stop)
	<-s.done
	err := s.flush()
	return errors.Join(err, s.db.Close())
}

// Epoch numbers this store's items together with their seq.
func (s *Store) Epoch() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// Ready signals after a commit added items or after items became sendable again.
func (s *Store) Ready() <-chan struct{} { return s.ready }

// Err is the error of the last failed background commit, nil once a commit succeeds again.
func (s *Store) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// Append queues an item and returns its seq. For Sync lanes it returns once the item is durable;
// otherwise the item becomes durable, and sendable, with the next batch commit. A failed commit
// keeps the item queued for the next one.
func (s *Store) Append(ctx context.Context, it Item) (uint32, error) {
	pol, ok := s.opts.Lanes[it.Lane]
	if !ok {
		return 0, fmt.Errorf("store: lane %v is not configured", it.Lane)
	}
	if it.DeviceTime.IsZero() {
		it.DeviceTime = s.opts.Now()
	}
	it.Payload = append([]byte(nil), it.Payload...)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrClosed
	}
	it.Seq = s.nextSeq
	s.nextSeq++
	s.pending = append(s.pending, it)
	batchFull := len(s.pending) >= s.opts.FlushItems
	var wait chan error
	if pol.Sync {
		wait = make(chan error, 1)
		s.waiters = append(s.waiters, wait)
	}
	s.mu.Unlock()
	if pol.Sync || batchFull {
		s.requestFlush()
	}
	if wait == nil {
		return it.Seq, nil
	}
	select {
	case err := <-wait:
		return it.Seq, err
	case <-ctx.Done():
		return it.Seq, ctx.Err()
	}
}

// Flush commits everything waiting now.
func (s *Store) Flush() error { return s.flush() }

func (s *Store) requestFlush() {
	select {
	case s.flushReq <- struct{}{}:
	default:
	}
}

func (s *Store) signalReady() {
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *Store) run() {
	defer close(s.done)
	t := time.NewTicker(s.opts.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
		case <-s.flushReq:
		}
		err := s.flush()
		s.mu.Lock()
		s.lastErr = err
		s.mu.Unlock()
	}
}

func (s *Store) flush() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	items, waiters := s.pending, s.waiters
	delThrough, delSeqs := s.delThrough, s.delSeqs
	s.pending, s.waiters, s.delThrough, s.delSeqs = nil, nil, 0, nil
	stats := make(map[Lane]laneStat, len(s.lanes))
	for l, st := range s.lanes {
		stats[l] = *st
	}
	s.mu.Unlock()

	var res commitResult
	var err error
	if len(items) > 0 || delThrough > 0 || len(delSeqs) > 0 {
		res, err = s.commit(items, delThrough, delSeqs, stats)
	}

	s.mu.Lock()
	if err != nil {
		s.pending = append(items, s.pending...)
		s.delThrough = max(s.delThrough, delThrough)
		s.delSeqs = append(delSeqs, s.delSeqs...)
	} else {
		for l, st := range res.stats {
			ls := s.lanes[l]
			if ls == nil {
				ls = &laneStat{}
				s.lanes[l] = ls
			}
			ls.items, ls.bytes = st.items, st.bytes
			ls.dropped += res.dropped[l]
		}
		for _, seq := range res.removed {
			delete(s.inflight, seq)
			if seq <= s.ackedThrough {
				delete(s.acked, seq)
			}
		}
		for _, seq := range delSeqs {
			delete(s.acked, seq)
		}
	}
	s.mu.Unlock()
	for _, w := range waiters {
		w <- err
	}
	if err == nil && len(items) > 0 {
		s.signalReady()
	}
	if err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

type commitResult struct {
	stats   map[Lane]laneStat
	dropped map[Lane]uint64
	removed []uint32
}

func (s *Store) commit(items []Item, delThrough uint32, delSeqs []uint32, stats map[Lane]laneStat) (commitResult, error) {
	ctx := context.Background()
	res := commitResult{stats: stats, dropped: map[Lane]uint64{}}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }()
	c := &committer{tx: tx, ctx: ctx, res: &res}

	if delThrough > 0 {
		if err := c.remove(`DELETE FROM items WHERE seq <= ? RETURNING seq, lane, size`, delThrough); err != nil {
			return res, err
		}
	}
	for _, seq := range delSeqs {
		if err := c.remove(`DELETE FROM items WHERE seq = ? RETURNING seq, lane, size`, seq); err != nil {
			return res, err
		}
	}
	touched := map[Lane]bool{}
	for _, it := range items {
		pol := s.opts.Lanes[it.Lane]
		if pol.LatestPerKey && it.Key != "" {
			if err := c.remove(`DELETE FROM items WHERE lane = ? AND key = ? RETURNING seq, lane, size`, it.Lane, it.Key); err != nil {
				return res, err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO items (seq, lane, key, rank, device_time, unsynced, size, payload) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			it.Seq, it.Lane, it.Key, it.Rank, it.DeviceTime.UnixNano(), it.ClockUnsynced, len(it.Payload), it.Payload); err != nil {
			return res, err
		}
		st := res.stats[it.Lane]
		st.items++
		st.bytes += int64(len(it.Payload))
		res.stats[it.Lane] = st
		touched[it.Lane] = true
	}
	for lane := range touched {
		if err := c.enforce(lane, s.opts.Lanes[lane]); err != nil {
			return res, err
		}
	}
	if len(items) > 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO meta (k, v) VALUES ('seq', ?) ON CONFLICT (k) DO UPDATE SET v = excluded.v`,
			items[len(items)-1].Seq); err != nil {
			return res, err
		}
	}
	return res, tx.Commit()
}

type committer struct {
	tx  *sql.Tx
	ctx context.Context
	res *commitResult
}

func (c *committer) remove(query string, args ...any) error {
	_, err := c.removeCounted(query, args...)
	return err
}

func (c *committer) removeCounted(query string, args ...any) (int, error) {
	rows, err := c.tx.QueryContext(c.ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var seq uint32
		var lane Lane
		var size int64
		if err := rows.Scan(&seq, &lane, &size); err != nil {
			return n, err
		}
		st := c.res.stats[lane]
		st.items--
		st.bytes -= size
		c.res.stats[lane] = st
		c.res.removed = append(c.res.removed, seq)
		n++
	}
	return n, rows.Err()
}

func (c *committer) drop(lane Lane, query string, args ...any) (int, error) {
	n, err := c.removeCounted(query, args...)
	c.res.dropped[lane] += uint64(n)
	return n, err
}

func (c *committer) enforce(lane Lane, pol LanePolicy) error {
	if pol.MaxItems > 0 {
		if over := c.res.stats[lane].items - pol.MaxItems; over > 0 {
			if _, err := c.drop(lane, `DELETE FROM items WHERE seq IN
				(SELECT seq FROM items WHERE lane = ? ORDER BY rank, seq LIMIT ?) RETURNING seq, lane, size`, lane, over); err != nil {
				return err
			}
		}
	}
	for pol.MaxBytes > 0 && c.res.stats[lane].bytes > pol.MaxBytes {
		if pol.Overflow == Thin && c.res.stats[lane].items >= 4 {
			n, err := c.drop(lane, `DELETE FROM items WHERE seq IN (
				SELECT seq FROM (
					SELECT seq, ROW_NUMBER() OVER (ORDER BY seq) AS rn FROM items WHERE lane = ? ORDER BY seq LIMIT ?
				) WHERE rn % 2 = 0) RETURNING seq, lane, size`, lane, c.res.stats[lane].items/2)
			if err != nil {
				return err
			}
			if n > 0 {
				continue
			}
		}
		if _, err := c.drop(lane, `DELETE FROM items WHERE seq IN
			(SELECT seq FROM items WHERE lane = ? ORDER BY rank, seq LIMIT 1) RETURNING seq, lane, size`, lane); err != nil {
			return err
		}
	}
	return nil
}

// Take hands out up to n committed items that are neither in flight nor acknowledged, ALARM
// first and oldest first within a lane, and marks them in flight.
func (s *Store) Take(n int) ([]Item, error) {
	if n <= 0 {
		return nil, nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	skip := len(s.inflight) + len(s.acked)
	through := s.ackedThrough
	s.mu.Unlock()

	rows, err := s.db.QueryContext(context.Background(),
		`SELECT seq, lane, key, rank, device_time, unsynced, payload FROM items WHERE seq > ? ORDER BY lane, seq LIMIT ?`,
		through, n+skip)
	if err != nil {
		return nil, fmt.Errorf("store: take: %w", err)
	}
	var found []Item
	for rows.Next() {
		var it Item
		var dt int64
		if err := rows.Scan(&it.Seq, &it.Lane, &it.Key, &it.Rank, &dt, &it.ClockUnsynced, &it.Payload); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: take: %w", err)
		}
		it.DeviceTime = time.Unix(0, dt)
		found = append(found, it)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: take: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	out := make([]Item, 0, min(n, len(found)))
	for _, it := range found {
		if len(out) == n {
			break
		}
		if _, busy := s.inflight[it.Seq]; busy {
			continue
		}
		if _, done := s.acked[it.Seq]; done || it.Seq <= s.ackedThrough {
			continue
		}
		s.inflight[it.Seq] = now
		out = append(out, it)
	}
	return out, nil
}

// InFlight is the number of items handed out and not yet acknowledged or released.
func (s *Store) InFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inflight)
}

// Unmark returns items that were taken but not sent. Unlike Release it does not signal Ready, so
// a sender whose link refused them waits for the next event instead of spinning.
func (s *Store) Unmark(seqs ...uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, seq := range seqs {
		delete(s.inflight, seq)
	}
}

// Release makes every item in flight sendable again, e.g. after the session was lost.
func (s *Store) Release() {
	s.mu.Lock()
	clear(s.inflight)
	s.mu.Unlock()
	s.signalReady()
}

// Retry makes items in flight for longer than timeout sendable again and returns how many.
func (s *Store) Retry(timeout time.Duration) int {
	s.mu.Lock()
	cutoff := s.opts.Now().Add(-timeout)
	n := 0
	for seq, at := range s.inflight {
		if at.Before(cutoff) {
			delete(s.inflight, seq)
			n++
		}
	}
	s.mu.Unlock()
	if n > 0 {
		s.signalReady()
	}
	return n
}

// HeldFrom is the lowest seq of the current epoch the store still holds unacknowledged, 0 when it
// holds none. Every seq below it was acknowledged or dropped for good and is never sent again.
func (s *Store) HeldFrom() (uint32, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrClosed
	}
	through := s.ackedThrough
	acked := len(s.acked)
	s.mu.Unlock()

	rows, err := s.db.QueryContext(context.Background(),
		`SELECT seq FROM items WHERE seq > ? ORDER BY seq LIMIT ?`, through, acked+1)
	if err != nil {
		return 0, fmt.Errorf("store: held from: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var seqs []uint32
	for rows.Next() {
		var seq uint32
		if err := rows.Scan(&seq); err != nil {
			return 0, fmt.Errorf("store: held from: %w", err)
		}
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: held from: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, seq := range seqs {
		if _, done := s.acked[seq]; !done && seq > s.ackedThrough {
			return seq, nil
		}
	}
	return 0, nil
}

// Ack drops what the platform acknowledged: everything up to the cumulative seq and every seq
// the selective bitmap names, counted from the selective base when the ack sets one. Acks for another epoch are ignored. Removal from disk happens
// with the next batch commit; acknowledged items are never handed out again.
func (s *Store) Ack(a *edgelinkv3.Ack) {
	if a == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.GetEpoch() != s.epoch {
		return
	}
	if cum := a.GetCumulativeSeq(); cum > s.ackedThrough {
		s.ackedThrough = cum
		s.delThrough = max(s.delThrough, cum)
		for seq := range s.inflight {
			if seq <= cum {
				delete(s.inflight, seq)
			}
		}
		for seq := range s.acked {
			if seq <= cum {
				delete(s.acked, seq)
			}
		}
	}
	base := a.GetCumulativeSeq() + 1
	if a.GetSelectiveBase() != 0 {
		base = a.GetSelectiveBase() + 1
	}
	for i, b := range a.GetSelectiveBitmap() {
		for bit := range 8 {
			if b&(1<<bit) == 0 {
				continue
			}
			seq := base + uint32(i*8+bit)
			if seq <= s.ackedThrough {
				continue
			}
			if _, dup := s.acked[seq]; dup {
				continue
			}
			s.acked[seq] = struct{}{}
			s.delSeqs = append(s.delSeqs, seq)
			delete(s.inflight, seq)
		}
	}
}

// AdvanceEpoch moves the store to epoch when it is behind, e.g. after a reset store started
// below the epoch the platform last saw. Queued items keep their seq under the new epoch.
func (s *Store) AdvanceEpoch(epoch uint32) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if epoch <= s.epoch {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if _, err := s.db.ExecContext(context.Background(), `UPDATE meta SET v = ? WHERE k = 'epoch'`, epoch); err != nil {
		return fmt.Errorf("store: advance epoch: %w", err)
	}
	s.mu.Lock()
	s.epoch = epoch
	s.ackedThrough = 0
	clear(s.acked)
	clear(s.inflight)
	s.mu.Unlock()
	s.signalReady()
	return nil
}

// Backlog reports every configured lane, in drain order.
func (s *Store) Backlog() ([]LaneBacklog, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT lane, MIN(device_time) FROM items GROUP BY lane`)
	if err != nil {
		return nil, fmt.Errorf("store: backlog: %w", err)
	}
	oldest := map[Lane]time.Time{}
	for rows.Next() {
		var lane Lane
		var dt int64
		if err := rows.Scan(&lane, &dt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: backlog: %w", err)
		}
		oldest[lane] = time.Unix(0, dt)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: backlog: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []LaneBacklog
	for lane := edgelinkv3.Lane_LANE_ALARM; lane <= edgelinkv3.Lane_LANE_BULK; lane++ {
		if _, ok := s.opts.Lanes[lane]; !ok {
			continue
		}
		b := LaneBacklog{Lane: lane, Oldest: oldest[lane]}
		if st := s.lanes[lane]; st != nil {
			b.Items, b.Bytes, b.Dropped = st.items, st.bytes, st.dropped
		}
		out = append(out, b)
	}
	return out, nil
}
