package tiny

import (
	"errors"
	"fmt"
	"sync"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
)

var (
	// ErrReplay marks an authentic frame the receiver has already seen (spec §6.1).
	ErrReplay = errors.New("tiny: replayed frame")
	// ErrExpired marks a COMMAND received after its expiry; the node answers it REJECTED, command.expired.
	ErrExpired = errors.New("tiny: command expired")
	// ErrDuplicateCommand marks a COMMAND whose downlink seq this epoch already ran (spec §7.2).
	ErrDuplicateCommand = errors.New("tiny: command already received")
)

// Inbound is what a downlink frame carried: exactly one of Command and Cancel is set.
type Inbound struct {
	Header  Header
	Command *Command
	Cancel  *CommandCancel
}

// Receiver opens the platform's frames for one node under its root key: it derives K_epoch for the
// epoch each frame names, drops replays, and hands out each COMMAND of an epoch once. It is safe for
// concurrent use.
type Receiver struct {
	mu     sync.Mutex
	root   []byte
	suite  Suite
	keys   map[uint16]*EpochKey
	window ReplayWindow
	ran    map[uint16]map[uint16]struct{}
}

// NewReceiver takes the node's K_root and the suite the platform seals with; the key is copied.
func NewReceiver(root []byte, suite Suite) (*Receiver, error) {
	if len(root) != RootKeySize {
		return nil, fmt.Errorf("tiny: root key must be %d bytes, got %d", RootKeySize, len(root))
	}
	if !suite.valid() {
		return nil, fmt.Errorf("tiny: unsupported suite 0x%02x", uint8(suite))
	}
	return &Receiver{root: append([]byte(nil), root...), suite: suite, keys: map[uint16]*EpochKey{},
		ran: map[uint16]map[uint16]struct{}{}}, nil
}

// Receive authenticates a downlink frame and decodes the COMMAND or COMMAND_CANCEL it carries. On
// ErrExpired and ErrDuplicateCommand the Inbound still names the command, so the caller can answer it.
func (r *Receiver) Receive(frame []byte, now time.Time) (Inbound, error) {
	h, err := ParseHeader(frame)
	if err != nil {
		return Inbound{}, err
	}
	if h.Suite != r.suite {
		return Inbound{}, ErrWrongKey
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key, err := r.key(h.Epoch)
	if err != nil {
		return Inbound{}, err
	}
	m, err := key.Open(Downlink, frame)
	if err != nil {
		return Inbound{}, err
	}
	if !r.window.Accept(h.Epoch, m.Counter) {
		return Inbound{}, ErrReplay
	}
	if m.Lane != edgelinkv3.Lane_LANE_COMMAND_REPLY {
		return Inbound{}, fmt.Errorf("%w: schema %d in lane %d", ErrMalformed, m.Schema, m.Lane)
	}
	switch {
	case m.Schema == SchemaCommandCancel:
		c, err := DecodeCommandCancel(m.Body)
		if err != nil {
			return Inbound{}, err
		}
		return Inbound{Header: h, Cancel: &c}, nil
	case IsCommandSchema(m.Schema):
		c, err := DecodeCommand(m.Schema, m.Body)
		if err != nil {
			return Inbound{}, err
		}
		in := Inbound{Header: h, Command: &c}
		if !now.Before(c.Expires) {
			return in, ErrExpired
		}
		if r.alreadyRan(h.Epoch, c.DownlinkSeq) {
			return in, ErrDuplicateCommand
		}
		return in, nil
	}
	return Inbound{}, fmt.Errorf("%w: schema %d is not a downlink schema", ErrMalformed, m.Schema)
}

// Zero overwrites the root key; the receiver opens nothing afterwards.
func (r *Receiver) Zero() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.root)
	r.keys = map[uint16]*EpochKey{}
}

func (r *Receiver) key(epoch uint16) (*EpochKey, error) {
	if k, ok := r.keys[epoch]; ok {
		return k, nil
	}
	k, err := DeriveEpoch(r.root, r.suite, epoch)
	if err != nil {
		return nil, err
	}
	for e := range r.keys {
		if e+1 < epoch {
			delete(r.keys, e)
			delete(r.ran, e)
		}
	}
	r.keys[epoch] = k
	return k, nil
}

func (r *Receiver) alreadyRan(epoch uint16, seq uint16) bool {
	seqs, ok := r.ran[epoch]
	if !ok {
		seqs = map[uint16]struct{}{}
		r.ran[epoch] = seqs
	}
	if _, dup := seqs[seq]; dup {
		return true
	}
	seqs[seq] = struct{}{}
	return false
}
