package rns

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type vectors struct {
	RNSVersion           string `json:"rns_version"`
	PrivateKey           string `json:"private_key"`
	PublicKey            string `json:"public_key"`
	IdentityHash         string `json:"identity_hash"`
	NameHash             string `json:"name_hash"`
	DestinationHash      string `json:"destination_hash"`
	PlainDestinationHash string `json:"plain_destination_hash"`
	Message              string `json:"message"`
	Signature            string `json:"signature"`
	Plaintext            string `json:"plaintext"`
	Ciphertext           string `json:"ciphertext"`
	AnnounceRaw          string `json:"announce_raw"`
	AnnounceHash         string `json:"announce_hash"`
	DataRaw              string `json:"data_raw"`
	DataHash             string `json:"data_hash"`
}

// The vectors come from Reticulum 0.9.4 (testdata/gen_vectors.py).
func loadVectors(t *testing.T) vectors {
	t.Helper()
	b, err := os.ReadFile("testdata/vectors_0.9.4.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v.RNSVersion != "0.9.4" {
		t.Fatalf("vectors from rns %s, want 0.9.4", v.RNSVersion)
	}
	return v
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func vectorIdentity(t *testing.T, v vectors) *Identity {
	t.Helper()
	id, err := IdentityFromPrivateKey(unhex(t, v.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestIdentityKeysAndHashMatchPython(t *testing.T) {
	v := loadVectors(t)
	id := vectorIdentity(t, v)
	if got := hex.EncodeToString(id.PublicKey()); got != v.PublicKey {
		t.Fatalf("public key %s, want %s", got, v.PublicKey)
	}
	if got := id.HexHash(); got != v.IdentityHash {
		t.Fatalf("identity hash %s, want %s", got, v.IdentityHash)
	}
	if !bytes.Equal(id.PrivateKey(), unhex(t, v.PrivateKey)) {
		t.Fatal("private key does not round-trip")
	}
}

func TestDestinationHashesMatchPython(t *testing.T) {
	v := loadVectors(t)
	d, err := NewSingle(vectorIdentity(t, v), "zequent", "edge", "link")
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(d.NameHash); got != v.NameHash {
		t.Fatalf("name hash %s, want %s", got, v.NameHash)
	}
	if got := hex.EncodeToString(d.Hash); got != v.DestinationHash {
		t.Fatalf("destination hash %s, want %s", got, v.DestinationHash)
	}
	plain, err := DestinationHash(nil, "zequent", "edge", "link")
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(plain); got != v.PlainDestinationHash {
		t.Fatalf("plain destination hash %s, want %s", got, v.PlainDestinationHash)
	}
	if _, err := NameHash("zequent.edge"); err == nil {
		t.Fatal("a dot in the app name must be refused")
	}
}

func TestSignatureMatchesPythonAndVerifies(t *testing.T) {
	v := loadVectors(t)
	id := vectorIdentity(t, v)
	sig, err := id.Sign(unhex(t, v.Message))
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(sig); got != v.Signature {
		t.Fatalf("signature %s, want %s", got, v.Signature)
	}
	remote, _ := IdentityFromPublicKey(unhex(t, v.PublicKey))
	if !remote.Verify(sig, unhex(t, v.Message)) || remote.Verify(sig, []byte("other")) {
		t.Fatal("verification is wrong")
	}
}

func TestDecryptsWhatPythonEncrypted(t *testing.T) {
	v := loadVectors(t)
	plaintext, err := vectorIdentity(t, v).Decrypt(unhex(t, v.Ciphertext))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plaintext, unhex(t, v.Plaintext)) {
		t.Fatalf("plaintext %q", plaintext)
	}
	tampered := unhex(t, v.Ciphertext)
	tampered[len(tampered)-1] ^= 1
	if _, err := vectorIdentity(t, v).Decrypt(tampered); err == nil {
		t.Fatal("a tampered token must be refused")
	}
}

func TestEncryptDecryptRoundTripAndRatchet(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	remote, _ := IdentityFromPublicKey(id.PublicKey())
	for _, size := range []int{0, 1, 15, 16, 17, EncryptedMDU} {
		msg := bytes.Repeat([]byte{0xAB}, size)
		ct, err := remote.Encrypt(msg, nil)
		if err != nil {
			t.Fatal(err)
		}
		pt, err := id.Decrypt(ct)
		if err != nil || !bytes.Equal(pt, msg) {
			t.Fatalf("size %d: %v", size, err)
		}
	}
	ratchet, _ := NewIdentity()
	ct, err := remote.Encrypt([]byte("for the ratchet"), ratchet.PublicKey()[:32])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := id.Decrypt(ct); err == nil {
		t.Fatal("a packet for the ratchet must not decrypt with the identity key")
	}
}

func TestAnnounceFromPythonValidates(t *testing.T) {
	v := loadVectors(t)
	p, err := Unpack(unhex(t, v.AnnounceRaw))
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != PacketAnnounce || p.HeaderType != Header1 || p.DestType != DestSingle {
		t.Fatalf("header %+v", p)
	}
	a, err := ParseAnnounce(p)
	if err != nil {
		t.Fatal(err)
	}
	if a.Identity.HexHash() != v.IdentityHash || string(a.AppData) != "app-data" || a.Ratchet != nil {
		t.Fatalf("announce %+v", a)
	}
	h, _ := p.Hash()
	if got := hex.EncodeToString(h); got != v.AnnounceHash {
		t.Fatalf("announce hash %s, want %s", got, v.AnnounceHash)
	}
	repacked, _ := p.Pack()
	if !bytes.Equal(repacked, unhex(t, v.AnnounceRaw)) {
		t.Fatal("announce does not repack byte for byte")
	}
	p.Data[len(p.Data)-1] ^= 1
	if _, err := ParseAnnounce(p); err == nil {
		t.Fatal("a modified announce must be refused")
	}
}

func TestOwnAnnounceValidates(t *testing.T) {
	v := loadVectors(t)
	d, _ := NewSingle(vectorIdentity(t, v), "zequent", "edge", "link")
	p, err := d.Announce([]byte("node-7"), false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Pack()
	if err != nil {
		t.Fatal(err)
	}
	back, _ := Unpack(raw)
	a, err := ParseAnnounce(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.AppData) != "node-7" || !bytes.Equal(a.DestinationHash, d.Hash) {
		t.Fatalf("announce %+v", a)
	}
}

func TestDataPacketFromPythonAndHeader2(t *testing.T) {
	v := loadVectors(t)
	p, err := Unpack(unhex(t, v.DataRaw))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := p.Hash()
	if got := hex.EncodeToString(h); got != v.DataHash {
		t.Fatalf("data hash %s, want %s", got, v.DataHash)
	}
	pt, err := vectorIdentity(t, v).Decrypt(p.Data)
	if err != nil || string(pt) != "x" {
		t.Fatalf("data %q %v", pt, err)
	}
	// In transport the same packet carries a transport id and keeps its hash.
	p.HeaderType, p.TransportType, p.TransportID, p.Hops = Header2, TransportTransport, bytes.Repeat([]byte{7}, 16), 3
	raw, err := p.Pack()
	if err != nil {
		t.Fatal(err)
	}
	back, _ := Unpack(raw)
	h2, _ := back.Hash()
	if !bytes.Equal(h, h2) || !bytes.Equal(back.TransportID, p.TransportID) || back.Hops != 3 {
		t.Fatal("HEADER_2 packet does not round-trip with the same hash")
	}
	if _, err := (&Packet{DestinationHash: p.DestinationHash, Data: make([]byte, MTU)}).Pack(); err == nil {
		t.Fatal("a packet above the MTU must be refused")
	}
}

func TestHDLCFramingAcrossReads(t *testing.T) {
	packet := append(bytes.Repeat([]byte{0x01}, HeaderMinSize), hdlcFlag, hdlcEsc, 0x02)
	other := bytes.Repeat([]byte{0x03}, HeaderMinSize+5)
	stream := append(hdlcFrame(packet), hdlcFrame(other)...)
	stream = append(stream, hdlcFrame([]byte{0x01, 0x02})...)
	d := deframer{maxFrame: hwMTU}
	var got [][]byte
	for _, b := range stream {
		got = append(got, d.write([]byte{b})...)
	}
	if len(got) != 2 || !bytes.Equal(got[0], packet) || !bytes.Equal(got[1], other) {
		t.Fatalf("got %d frames", len(got))
	}
}
