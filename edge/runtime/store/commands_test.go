package store_test

import (
	"fmt"
	"testing"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

func TestClaimedCommandsSurviveARestart(t *testing.T) {
	path := tempDB(t)
	s := open(t, path, store.Options{})
	if ok, err := s.ClaimCommand("ce-1", []byte("p1")); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	if ok, err := s.ClaimCommand("ce-1", []byte("p1")); err != nil || ok {
		t.Fatalf("second claim = %v, %v", ok, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = open(t, path, store.Options{})
	if ok, _ := s.ClaimCommand("ce-1", nil); ok {
		t.Fatal("a claimed id was forgotten across a restart")
	}
	unfinished, err := s.UnfinishedCommands()
	if err != nil || len(unfinished) != 1 || unfinished[0].ID != "ce-1" || string(unfinished[0].Payload) != "p1" {
		t.Fatalf("unfinished = %v, %v", unfinished, err)
	}
	if err := s.FinishCommand("ce-1"); err != nil {
		t.Fatal(err)
	}
	if unfinished, _ := s.UnfinishedCommands(); len(unfinished) != 0 {
		t.Fatalf("unfinished after finish = %v", unfinished)
	}
}

func TestTheLast256FinishedCommandsAreRemembered(t *testing.T) {
	s := open(t, tempDB(t), store.Options{})
	for i := range store.RememberedCommands + 10 {
		id := fmt.Sprintf("ce-%d", i)
		if ok, err := s.ClaimCommand(id, nil); err != nil || !ok {
			t.Fatalf("claim %s = %v, %v", id, ok, err)
		}
		if err := s.FinishCommand(id); err != nil {
			t.Fatal(err)
		}
	}
	if ok, _ := s.ClaimCommand(fmt.Sprintf("ce-%d", 10), nil); ok {
		t.Fatal("one of the last 256 ids was forgotten")
	}
	if ok, _ := s.ClaimCommand("ce-0", nil); !ok {
		t.Fatal("an id older than the last 256 is still remembered")
	}
}

func TestAForgottenClaimMayBeClaimedAgain(t *testing.T) {
	s := open(t, tempDB(t), store.Options{})
	if ok, _ := s.ClaimCommand("ce-1", nil); !ok {
		t.Fatal("not claimed")
	}
	if err := s.ForgetCommand("ce-1"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.ClaimCommand("ce-1", nil); !ok {
		t.Fatal("a forgotten claim stays claimed")
	}
}
