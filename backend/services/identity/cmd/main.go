package main

import (
	"banya.local/backend/internal/platform"
	identity "banya.local/backend/services/identity/internal"
)

func main() { platform.Run(identity.Start) }
