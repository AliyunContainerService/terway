//go:build !linux

package instance

func EfloPopulate() *Instance {
	return &Instance{}
}
