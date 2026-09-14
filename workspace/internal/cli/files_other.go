//go:build !unix

package cli

import "os"

func processUmask() os.FileMode { return 0 }
