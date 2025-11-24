package impls

// PortAllocator резервирует порты на хосте.
type PortAllocator interface {
	Allocate(count int) ([]int, error)
}
