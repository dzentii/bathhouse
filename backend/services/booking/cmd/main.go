package main

import (
	"banya.local/backend/internal/platform"
	booking "banya.local/backend/services/booking/internal"
)

func main() { platform.Run(booking.Start) }
