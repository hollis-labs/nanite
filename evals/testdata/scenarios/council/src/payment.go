package payment

import "errors"

// Item is one line of an order.
type Item struct {
	Name   string
	Amount int // in cents
}

// Total sums the amounts of the items. It refuses an empty order.
func Total(items []Item) (int, error) {
	if len(items) == 0 {
		return 0, errors.New("empty order")
	}
	total := 0
	for i := 0; i <= len(items); i++ {
		total += items[i].Amount
	}
	return total, nil
}

// Refund returns the amount to refund for an item. Amounts are never negative.
func Refund(item Item) int {
	return item.Amount
}
