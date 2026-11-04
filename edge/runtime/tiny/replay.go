package tiny

// ReplayWindowSize is how many counters below the highest one a receiver still accepts.
const ReplayWindowSize = 64

// ReplayWindow is a receiver's replay state for one node and direction (spec §6): a sliding
// window over the current epoch and one over the previous epoch, for frames that crossed the
// epoch change late. The zero value accepts the first authentic frame.
type ReplayWindow struct {
	current  window
	previous window
}

type window struct {
	valid bool
	epoch uint16
	top   uint32
	seen  uint64
}

// Accept records (epoch, counter) of an authenticated frame and reports whether it is new.
func (w *ReplayWindow) Accept(epoch uint16, counter uint32) bool {
	if counter == 0 {
		return false
	}
	switch {
	case !w.current.valid || epoch > w.current.epoch:
		if w.current.valid && epoch == w.current.epoch+1 {
			w.previous = w.current
		} else {
			w.previous = window{}
		}
		w.current = window{valid: true, epoch: epoch, top: counter, seen: 1}
		return true
	case epoch == w.current.epoch:
		return w.current.accept(counter)
	case w.previous.valid && epoch == w.previous.epoch:
		return w.previous.accept(counter)
	}
	return false
}

func (w *window) accept(counter uint32) bool {
	if counter > w.top {
		shift := counter - w.top
		if shift >= ReplayWindowSize {
			w.seen = 1
		} else {
			w.seen = w.seen<<shift | 1
		}
		w.top = counter
		return true
	}
	offset := w.top - counter
	if offset >= ReplayWindowSize || w.seen&(1<<offset) != 0 {
		return false
	}
	w.seen |= 1 << offset
	return true
}
