package secret

type Secret string

func (s Secret) String() string {
	return "******"
}

func (s Secret) GoString() string {
	return "******"
}

func (s Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"******"`), nil
}
