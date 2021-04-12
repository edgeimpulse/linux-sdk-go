package edgeimpulse

import (
	"io/ioutil"
	"os"
)

// TempDir returns either a temporary directory in /dev/shm (if it exists), or
// otherwise in the OS default temporary directory.
func TempDir() (string, error) {
	// Attempt to make temp dir for runner in /dev/shm. If that fails (eg
	// no permission), then attempt at OS default temp dir.
	// Check if /dev/shm exists first. Don't want to accidentially create a
	// directory in /dev (if someones runs this as root).
	if fi, err := os.Stat("/dev/shm"); err == nil && fi.IsDir() {
		dir, err := ioutil.TempDir("/dev/shm", "edge-impulse-cli")
		if err == nil {
			return dir, nil
		}
	}
	return ioutil.TempDir("", "edge-impulse-cli")
}
