// +build profile

package prof

import (
	"github.com/pkg/profile"
)

const ProfileEnabled = true

func StartProfile(path string) interface {
	Stop()
} {
	//debug.SetGCPercent(-1)
	//return profile.Start(profile.CPUProfile, profile.ProfilePath(path),
	//	profile.NoShutdownHook)
	return profile.Start(profile.MemProfile, profile.ProfilePath(path),
		profile.NoShutdownHook)
}
