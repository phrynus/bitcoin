package exchange

func (e *ExchangeInfo) Get(s string) *Symbol {
	return e.Symbols[s]
}
