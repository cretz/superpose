package superpose

type Config struct {
	// Required, and must be unique for each transformer change (this affects
	// cache)
	Version    string
	Dimensions []string
}

type Superpose struct {
	Transformer Transformer
}

func RunMain(config Config, runConfig RunMainConfig) {
}

func New(config Config) (*Superpose, error) {
	return &Superpose{}, nil
}

type RunMainConfig struct {
	AssumeToolexec bool
}

func (s *Superpose) RunMainErr(args []string, config RunMainConfig) error {
	panic("TODO")
}
