//go:build darwin

package media

// macOS can leave ctime on an open descriptor unchanged across renameatx_np
// while updating the path's stat result. Link count still distinguishes an
// unlinked producer inode from a same-size replacement.
func sameStrictFileIdentity(a, b fileIdentity) bool {
	return sameFileIdentity(a, b) && a.links == b.links
}
