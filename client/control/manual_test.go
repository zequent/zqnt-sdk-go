package control_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/zequent/zqnt-client-sdk-go/v3/control"
	commonv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/common/v3"
	controlv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/control/v3"
	edgev3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/edge/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (f *fakeRemoteControl) StreamManualControl(stream grpc.ClientStreamingServer[controlv3.StreamManualControlRequest, controlv3.StreamManualControlResponse]) error {
	var sn string
	var accepted int64
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			resp := &controlv3.StreamManualControlResponse{AcceptedInputs: accepted}
			if sn == "DRONE-MANUAL-OFF" {
				resp.Error = &commonv3.Error{Category: commonv3.ErrorCategory_ERROR_CATEGORY_PRECONDITION_FAILED,
					Code: "flight.manual.not_entered", Message: "enter manual control first"}
			}
			return stream.SendAndClose(resp)
		}
		if err != nil {
			return err
		}
		if sn == "" {
			sn = req.GetAsset().GetSn()
			if sn == "" {
				return status.Error(codes.InvalidArgument, "asset.sn is required")
			}
			if sn == "DRONE-OTHER-ORG" {
				return status.Error(codes.PermissionDenied, "asset of another organization")
			}
		}
		f.mu.Lock()
		f.manualInputs = append(f.manualInputs, req)
		f.mu.Unlock()
		accepted++
	}
}

func TestStreamManualControlNamesTheAssetOnceAndCountsTheInputs(t *testing.T) {
	fake := newFake()
	stick, err := dial(t, fake, "tok").StreamManualControl(timeout(t), "DRONE-1")
	if err != nil {
		t.Fatalf("StreamManualControl: %v", err)
	}
	if err := stick.Send(&edgev3.ManualControlInput{Pitch: 0.2, Throttle: 0.5}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := stick.Send(&edgev3.ManualControlInput{Throttle: 0.4, GimbalPitch: proto.Float32(-30)}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	accepted, err := stick.Close()
	if err != nil || accepted != 2 {
		t.Fatalf("Close = %d, %v", accepted, err)
	}
	if again, err := stick.Close(); again != 2 || err != nil {
		t.Fatalf("second Close = %d, %v", again, err)
	}
	if err := stick.Send(&edgev3.ManualControlInput{}); !errors.Is(err, control.ErrManualControlClosed) {
		t.Fatalf("Send after Close = %v", err)
	}
	first, second := fake.manualInputs[0], fake.manualInputs[1]
	if first.GetAsset().GetSn() != "DRONE-1" || first.GetInput().GetThrottle() != 0.5 || first.GetInput().GimbalPitch != nil {
		t.Fatalf("first = %v", first)
	}
	if second.GetAsset() != nil || second.GetInput().GetGimbalPitch() != -30 {
		t.Fatalf("second = %v", second)
	}
}

func TestARefusedManualControlSessionKeepsItsStatusCode(t *testing.T) {
	stick, err := dial(t, newFake(), "tok").StreamManualControl(timeout(t), "DRONE-OTHER-ORG")
	if err != nil {
		t.Fatalf("StreamManualControl: %v", err)
	}
	_ = stick.Send(&edgev3.ManualControlInput{Throttle: 0.5})
	var sendErr error
	for range 100 {
		if sendErr = stick.Send(&edgev3.ManualControlInput{Throttle: 0.5}); sendErr != nil {
			break
		}
	}
	_, closeErr := stick.Close()
	for _, err := range []error{sendErr, closeErr} {
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestAnErrorInTheManualControlAnswerIsACommandError(t *testing.T) {
	stick, err := dial(t, newFake(), "tok").StreamManualControl(timeout(t), "DRONE-MANUAL-OFF")
	if err != nil {
		t.Fatalf("StreamManualControl: %v", err)
	}
	if err := stick.Send(&edgev3.ManualControlInput{Throttle: 0.5}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, err = stick.Close()
	var commandErr *control.CommandError
	if !errors.As(err, &commandErr) || commandErr.Category() != commonv3.ErrorCategory_ERROR_CATEGORY_PRECONDITION_FAILED {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "flight.manual.not_entered: enter manual control first") {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestStreamManualControlNeedsAnAsset(t *testing.T) {
	if _, err := dial(t, newFake(), "tok").StreamManualControl(timeout(t), ""); err == nil {
		t.Fatal("an empty serial must be refused")
	}
}
