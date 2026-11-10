package main

import (
	"banya.local/backend/internal/platform"
	catalog "banya.local/backend/services/catalog/internal"
)

func main() { platform.Run(catalog.Start) }
