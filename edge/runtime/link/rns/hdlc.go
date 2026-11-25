package rns

import "bytes"

// HDLC-style framing as Reticulum's TCP interfaces use it: a frame is the packet between two flag
// bytes, with flag and escape bytes escaped.
const (
	hdlcFlag    byte = 0x7E
	hdlcEsc     byte = 0x7D
	hdlcEscMask byte = 0x20
)

func hdlcFrame(packet []byte) []byte {
	out := make([]byte, 0, len(packet)+8)
	out = append(out, hdlcFlag)
	for _, b := range packet {
		switch b {
		case hdlcEsc, hdlcFlag:
			out = append(out, hdlcEsc, b^hdlcEscMask)
		default:
			out = append(out, b)
		}
	}
	return append(out, hdlcFlag)
}

func hdlcUnescape(frame []byte) []byte {
	out := make([]byte, 0, len(frame))
	for i := 0; i < len(frame); i++ {
		if frame[i] == hdlcEsc && i+1 < len(frame) {
			i++
			out = append(out, frame[i]^hdlcEscMask)
			continue
		}
		out = append(out, frame[i])
	}
	return out
}

// deframer collects bytes from a stream and returns complete packets. A closing flag also opens the
// next frame, and frames no longer than a minimal header are dropped, as on the Python side.
type deframer struct {
	buf      []byte
	maxFrame int
}

func (d *deframer) write(data []byte) [][]byte {
	d.buf = append(d.buf, data...)
	var packets [][]byte
	for {
		start := bytes.IndexByte(d.buf, hdlcFlag)
		if start < 0 {
			d.buf = d.buf[:0]
			return packets
		}
		end := bytes.IndexByte(d.buf[start+1:], hdlcFlag)
		if end < 0 {
			d.buf = d.buf[start:]
			if d.maxFrame > 0 && len(d.buf) > d.maxFrame {
				d.buf = d.buf[:0]
			}
			return packets
		}
		end += start + 1
		packet := hdlcUnescape(d.buf[start+1 : end])
		if len(packet) > HeaderMinSize {
			packets = append(packets, packet)
		}
		d.buf = d.buf[end:]
	}
}
