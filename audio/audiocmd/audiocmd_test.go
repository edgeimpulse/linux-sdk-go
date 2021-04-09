package audiocmd

import (
	"bytes"
	"testing"

	"github.com/edgeimpulse/linux-sdk-go/audio"
)

func TestParseAsoundCards(t *testing.T) {
	const s = ` 0 [PCH            ]: HDA-Intel - HDA Intel PCH
                      HDA Intel PCH at 0x2ffb018000 irq 152`

	r, err := parseAsoundCards(bytes.NewReader([]byte(s)))
	if err != nil {
		t.Fatalf("parsing asound cards: %v", err)
	}
	exp := audio.Device{ID: "hw:0,0", Name: "HDA-Intel - HDA Intel PCH"}
	if len(r) != 1 || r[0] != exp {
		t.Fatalf("unexpected result %#v, expected %#v", r, []audio.Device{exp})
	}
}

func TestParseSoxDevices(t *testing.T) {
	const s = `sox:      SoX v
time:     Mar 11 2021 16:17:56
issue:    macosx
uname:    Darwin host.name 19.6.0 Darwin Kernel Version 19.6.0: Mon Aug 31 22:12:52 PDT 2020; root:xnu-6153.141.2~1/RELEASE_X86_64 x86_64
compiler: gcc Apple LLVM 12.0.0 (clang-1200.0.32.29)
arch:     1288 48 88 L
sox INFO nulfile: sample rate not specified; using 48000

Input File     : '' (null)
Channels       : 1
Sample Rate    : 48000
Precision      : 32-bit

sox INFO coreaudio: Found Audio Device "Built-i"

sox INFO coreaudio: Found Audio Device "Built-i"

sox FAIL formats: can't open output file ` + "`" + `doesnotexist': can not open audio device
`

	r, err := parseSoxDevices(s)
	if err != nil {
		t.Fatalf("parsing sox devices: %v", err)
	}
	exp := audio.Device{ID: "Built-i", Name: "Built-i"}
	if len(r) != 1 || r[0] != exp {
		t.Fatalf("unexpected result %#v, expected %#v", r, []audio.Device{exp})
	}
}
