// Command lists what an asset can do and runs one of its commands, waiting for the outcome.
//
//	ZQNT_CLIENT_TOKEN=... go run ./examples/command DRONE-1 flight.takeoff '{"altitude": 40}'
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/zequent/zqnt-client-sdk-go/v3/config"
	"github.com/zequent/zqnt-client-sdk-go/v3/control"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: command <asset-sn> [command-id] [params-json]")
	}
	sn := os.Args[1]

	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	conn, err := cfg.Dial(cfg.RemoteControl)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	assets := control.New(conn)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	set, err := assets.ListCapabilities(ctx, sn)
	if err != nil {
		log.Fatal(err)
	}
	for _, capability := range set.GetCapabilities() {
		fmt.Printf("%-32s %-10s %s\n", capability.GetCommandId(), capability.GetSafety().GetRisk(), capability.GetDisplayName())
	}
	if len(os.Args) < 3 {
		return
	}

	var params control.Params
	if len(os.Args) > 3 {
		if err := json.Unmarshal([]byte(os.Args[3]), &params); err != nil {
			log.Fatalf("params: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	result, err := assets.ExecuteAndWait(ctx, sn, os.Args[2], params)
	var refused *control.CommandError
	if errors.As(err, &refused) {
		log.Fatalf("%v (category %s, retryable %t)", refused, refused.Category(), refused.Retryable())
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s %s %v\n", result.GetCommandExecutionId(), result.GetState(), result.GetResult().AsMap())
}
