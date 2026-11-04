package store

import (
	"context"
	"fmt"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
)

// ackWindow is the most seqs past the cumulative one a selective bitmap names.
const ackWindow = 4096

// SeenDownlink reports whether the platform's message id was already received. Ids of an older
// platform epoch count as seen.
func (s *Store) SeenDownlink(id *edgelinkv3.MessageId) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.downlink
	switch {
	case id.GetEpoch() < d.epoch:
		return true
	case id.GetEpoch() > d.epoch:
		return false
	case id.GetSeq() <= d.cum:
		return true
	}
	_, ok := d.above[id.GetSeq()]
	return ok
}

// MarkDownlink records a received downlink message durably.
func (s *Store) MarkDownlink(id *edgelinkv3.MessageId) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	d := downlinkState{epoch: s.downlink.epoch, cum: s.downlink.cum, above: make(map[uint32]struct{}, len(s.downlink.above))}
	for seq := range s.downlink.above {
		d.above[seq] = struct{}{}
	}
	s.mu.Unlock()

	switch {
	case id.GetEpoch() < d.epoch:
		return nil
	case id.GetEpoch() > d.epoch:
		d = downlinkState{epoch: id.GetEpoch(), above: map[uint32]struct{}{}}
	}
	if id.GetSeq() > d.cum {
		d.above[id.GetSeq()] = struct{}{}
	}
	for {
		if _, ok := d.above[d.cum+1]; !ok {
			break
		}
		delete(d.above, d.cum+1)
		d.cum++
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: downlink: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, kv := range [][2]any{{"dl_epoch", d.epoch}, {"dl_cum", d.cum}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta (k, v) VALUES (?, ?) ON CONFLICT (k) DO UPDATE SET v = excluded.v`, kv[0], kv[1]); err != nil {
			return fmt.Errorf("store: downlink: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM downlink`); err != nil {
		return fmt.Errorf("store: downlink: %w", err)
	}
	for seq := range d.above {
		if _, err := tx.ExecContext(ctx, `INSERT INTO downlink (seq) VALUES (?)`, seq); err != nil {
			return fmt.Errorf("store: downlink: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: downlink: %w", err)
	}
	s.mu.Lock()
	s.downlink = d
	s.mu.Unlock()
	return nil
}

// DownlinkAck is how far the node has received the platform's downlink.
func (s *Store) DownlinkAck() *edgelinkv3.Ack {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.downlink
	a := &edgelinkv3.Ack{Epoch: d.epoch, CumulativeSeq: d.cum}
	for seq := range d.above {
		i := seq - d.cum - 1
		if i >= ackWindow {
			continue
		}
		for uint32(len(a.SelectiveBitmap)) <= i/8 {
			a.SelectiveBitmap = append(a.SelectiveBitmap, 0)
		}
		a.SelectiveBitmap[i/8] |= 1 << (i % 8)
	}
	return a
}
