//go:build linux

package media

func sameStrictFileIdentity(a, b fileIdentity) bool {
	return sameFileIdentity(a, b) && a.links == b.links && a.changeSec == b.changeSec && a.changeNsec == b.changeNsec
}
