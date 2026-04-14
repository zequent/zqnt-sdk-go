package domains

// TakeOffRequest is the payload for a TakeOff command.
type TakeOffRequest struct {
	SN          string
	TID         string
	Coordinates Coordinates
}

// GoToRequest is the payload for a GoTo command.
type GoToRequest struct {
	SN          string
	TID         string
	Coordinates Coordinates
}

// ReturnToHomeRequest is the payload for a ReturnToHome command.
type ReturnToHomeRequest struct {
	SN       string
	TID      string
	Altitude *float32
}

// LookAtRequest is the payload for a LookAt (gimbal point) command.
type LookAtRequest struct {
	SN           string
	TID          string
	Lat          float64
	Lon          float64
	Alt          float32
	PayloadIndex *string
	Locked       *bool
}

// TakePhotoRequest is the payload for a TakePhoto command.
type TakePhotoRequest struct {
	SN  string
	TID string
}

// ManualControlInput carries a single joystick frame for streaming manual control.
// All axes are optional; absent fields are treated as neutral.
type ManualControlInput struct {
	SN          string
	Roll        *float32
	Pitch       *float32
	Yaw         *float32
	Throttle    *float32
	GimbalPitch *float32
}

// ChangeLensRequest is the payload for a ChangeLens command.
type ChangeLensRequest struct {
	SN   string
	TID  string
	Lens *string
}

// ChangeZoomRequest is the payload for a ChangeZoom command.
type ChangeZoomRequest struct {
	SN   string
	TID  string
	Lens *string
	Zoom *int32
}

// LiveStreamStartRequest is the payload for a StartLiveStream command.
type LiveStreamStartRequest struct {
	SN           string
	TID          string
	VideoID      string
	StreamServer string
	VideoType    string
}

// LiveStreamStopRequest is the payload for a StopLiveStream command.
type LiveStreamStopRequest struct {
	SN      string
	TID     string
	VideoID string
}

// GetDetectionsRequest is the payload for a GetDetections server-streaming call.
type GetDetectionsRequest struct {
	SN        string
	TID       string
	StreamURL *string
}

// DetectionResult is a single object detection within a frame.
type DetectionResult struct {
	ObjectID   string
	ObjectType string
	Confidence float32
	BoundingBox BoundingBox
}

// BoundingBox holds the pixel coordinates of a detected object.
type BoundingBox struct {
	X      float32
	Y      float32
	Width  float32
	Height float32
}
