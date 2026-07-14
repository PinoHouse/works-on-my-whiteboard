package evidence

import "runtime"

func CurrentEnvironment() Environment {
	return Environment{
		GoVersion:   normalizeWhitespace(runtime.Version()),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		CPU:         "unknown",
		LogicalCPUs: runtime.NumCPU(),
	}
}
