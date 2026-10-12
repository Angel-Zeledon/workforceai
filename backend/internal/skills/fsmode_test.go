package skills

import "io/fs"

// fsMode converts a unix st_mode (as stored in zip headers) to fs.FileMode.
func fsMode(m uint32) fs.FileMode {
	mode := fs.FileMode(m & 0o777)
	switch m & 0o170000 {
	case 0o120000:
		mode |= fs.ModeSymlink
	case 0o60000:
		mode |= fs.ModeDevice
	case 0o40000:
		mode |= fs.ModeDir
	}
	return mode
}
