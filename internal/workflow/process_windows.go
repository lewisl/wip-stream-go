package workflow

import "os"

// Without positive liveness evidence, preserve the owner for inspection.
func processAlive(pid int) bool       { return true }
func ownedFile(info os.FileInfo) bool { return true }
