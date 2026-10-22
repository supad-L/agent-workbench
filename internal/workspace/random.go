package workspace

import "crypto/rand"

func randomSuffix() string { return rand.Text() }
