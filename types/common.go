package types

// FactoryResIf define how object react
type FactoryResIf interface {
	GetID() string
	GetType() string
}

var _ FactoryResIf = (*FactoryRes)(nil)

// FactoryRes hold resource get from factory
type FactoryRes struct {
	ID   string
	Type string
}

// GetID the uid of this obj
func (f *FactoryRes) GetID() string {
	return f.ID
}

// GetType the type of obj. eg. ENI , ENIIP
func (f *FactoryRes) GetType() string {
	return f.Type
}
