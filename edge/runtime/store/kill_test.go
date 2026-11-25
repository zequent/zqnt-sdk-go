package store_test

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

const killChildEnv = "ZQNT_STORE_KILL_CHILD"

func TestSurvivesKillDuringWrites(t *testing.T) {
	if path := os.Getenv(killChildEnv); path != "" {
		writeUntilKilled(path)
		return
	}
	path := tempDB(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestSurvivesKillDuringWrites$")
	cmd.Env = append(os.Environ(), killChildEnv+"="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var durable []uint32
	lines := bufio.NewScanner(out)
	for lines.Scan() && len(durable) < 300 {
		if seq, err := strconv.ParseUint(lines.Text(), 10, 32); err == nil {
			durable = append(durable, uint32(seq))
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if len(durable) < 300 {
		t.Fatalf("child reported %d durable alarms", len(durable))
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var check string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity_check = %q, %v", check, err)
	}
	for _, seq := range durable {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM items WHERE seq = ?`, seq).Scan(&n); err != nil || n != 1 {
			t.Fatalf("durable alarm %d missing after kill (%v)", seq, err)
		}
	}
	var maxSeq uint32
	if err := db.QueryRow(`SELECT MAX(seq) FROM items`).Scan(&maxSeq); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s := open(t, path, store.Options{})
	next := appendN(t, s, alarm, 1)
	if next[0] <= maxSeq {
		t.Fatalf("seq %d reused after kill (max %d)", next[0], maxSeq)
	}
}

func writeUntilKilled(path string) {
	s, err := store.Open(path, store.Options{FlushInterval: 5 * time.Millisecond, NewEpoch: func() uint32 { return 7 }})
	if err != nil {
		os.Exit(2)
	}
	ctx := context.Background()
	for i := 0; ; i++ {
		if _, err := s.Append(ctx, store.Item{Lane: telemetry, Payload: make([]byte, 512)}); err != nil {
			os.Exit(3)
		}
		if i%3 == 0 {
			seq, err := s.Append(ctx, store.Item{Lane: alarm, Payload: []byte("alarm")})
			if err != nil {
				os.Exit(4)
			}
			fmt.Println(seq)
		}
	}
}
