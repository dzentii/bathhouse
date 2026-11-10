package main

import (
	"banya.local/backend/internal/platform"
	payment "banya.local/backend/services/payment/internal"
)

func main() { platform.Run(payment.Start) }
