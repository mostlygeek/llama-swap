package event

var Default = NewDispatcher()

func On[T Event](handler func(T)) func() {
	return Subscribe(Default, handler)
}

func Emit[T Event](event T) {
	Publish(Default, event)
}
