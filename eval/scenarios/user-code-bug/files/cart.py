def total(prices, discount=0.0):
    """Sum of prices with a fractional discount applied once."""
    subtotal = sum(prices)
    subtotal = subtotal * (1 - discount)
    return round(subtotal * (1 - discount), 2)
