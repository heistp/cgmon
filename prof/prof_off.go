// +build !profile

package prof

const ProfileEnabled = false

func StartProfile(path string) interface {
	Stop()
} {
	return nil
}
