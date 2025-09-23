package docker

import "context"

type Builder struct{}

func NewBuilder() (*Builder, error) {
	return &Builder{}, nil
}

func (b *Builder) BuildImage(ctx context.Context, contextPath, dockerfilePath, tag string) error {
	// Implementation here
	return nil
}
