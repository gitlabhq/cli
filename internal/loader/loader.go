package loader

type Loader[T any] struct {
	currentPage   int
	recentResults []T
	fetch         func(page int) ([]T, int, error)
	err           error
}

func New[T any](fetch func(page int) ([]T, int, error)) *Loader[T] {
	return &Loader[T]{
		currentPage: 1,
		fetch:       fetch,
	}
}

func (l *Loader[T]) Next() bool {
	if l.currentPage == 0 {
		return false
	}

	result, nextPage, err := l.fetch(l.currentPage)
	if err != nil {
		l.err = err
		return false
	}
	l.currentPage = nextPage
	l.recentResults = result
	return true
}

func (l *Loader[T]) Page() []T {
	return l.recentResults
}

func (l *Loader[T]) Err() error {
	return l.err
}
