package imagesnap

import (
	"reflect"
	"testing"

	"github.com/edgeimpulse/linux-sdk-go/image"
)

func TestParseDevices(t *testing.T) {
	const imagesnap0 = `Video Devices:
<AVCaptureDALDevice: 0x7fa2c7852fd0 [FaceTime HD Camera (Built-in)][0x8020000005ac8514]>
<AVCaptureDALDevice: 0x7fa2c78512f0 [FaceTime HD Camera (Display)][0x4015000005ac1112]>
<AVCaptureDALDevice: 0x7fa2c784f4e0 [Cam Link 4K #5][0x2000000fd90066]>
`

	devs0, err := parseDevices(imagesnap0)
	if err != nil {
		t.Fatalf("parsing old imagesnap output: %v", err)
	}
	exp0 := []image.Device{
		{ID: "FaceTime HD Camera (Built-in)", Name: "FaceTime HD Camera (Built-in)"},
		{ID: "FaceTime HD Camera (Display)", Name: "FaceTime HD Camera (Display)"},
		{ID: "Cam Link 4K #5", Name: "Cam Link 4K #5"},
	}
	if !reflect.DeepEqual(devs0, exp0) {
		t.Fatalf("imagesnap devices, got %v, expected %v", exp0, devs0)
	}

	const imagesnap1 = `Video Devices:
=> FaceTime HD Camera (Built-in)
`
	devs1, err := parseDevices(imagesnap1)
	if err != nil {
		t.Fatalf("parsing imagesnap output: %v", err)
	}
	exp1 := []image.Device{
		{ID: "FaceTime HD Camera (Built-in)", Name: "FaceTime HD Camera (Built-in)"},
	}
	if !reflect.DeepEqual(devs1, exp1) {
		t.Fatalf("imagesnap devices, got %v, expected %v", exp1, devs1)
	}
}
