//go:build !linux

package debuglog

func platformResources(r *Resources, _ string) {
	r.Error = "RSS, file descriptor and disk samples are available on Linux only"
}
