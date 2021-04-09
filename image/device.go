package image

// DeviceCap describes a capability of a device.
type DeviceCap struct {
	Width     int
	Height    int
	Framerate int
}

// Device is a camera device capable of recording images.
type Device struct {
	Name string
	ID   string
	Caps []DeviceCap
}
