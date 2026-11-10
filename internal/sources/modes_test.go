package sources

import "runtime"

func isUnixModes() bool { return runtime.GOOS != "windows" }
