package filesystem

import "os"

// OpenLocalRoot shares workspace path restrictions for read-only source anchors.
// It rejects network drives and reparse ancestors without acquiring the source's lock.
func OpenLocalRoot(name string) (*os.Root, error) {
	if err := checkLocalRoot(name); err != nil {
		return nil, err
	}
	return os.OpenRoot(name)
}
