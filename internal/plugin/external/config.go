package external

import (
	"errors"
	"time"
)

type Config struct {
	MessageBytes  int `toml:"message_bytes"`
	ControlQueue  int `toml:"control_queue"`
	ControlBytes  int `toml:"control_bytes"`
	PTYQueueBytes int `toml:"pty_queue_bytes"`
	InitializeMS  int `toml:"initialize_ms"`
	APIMS         int `toml:"api_ms"`
	RenderMS      int `toml:"render_ms"`
	CommandMS     int `toml:"command_ms"`
	InteractionMS int `toml:"interaction_ms"`
	ShutdownMS    int `toml:"shutdown_ms"`
}

func DefaultConfig() Config {
	return Config{8 << 20, 64, 16 << 20, 1 << 20, 5000, 2000, 1000, 30000, 900000, 2000}
}
func (c Config) Normalize() (Config, error) {
	d := DefaultConfig()
	p := []*int{&c.MessageBytes, &c.ControlQueue, &c.ControlBytes, &c.PTYQueueBytes, &c.InitializeMS, &c.APIMS, &c.RenderMS, &c.CommandMS, &c.InteractionMS, &c.ShutdownMS}
	q := []int{d.MessageBytes, d.ControlQueue, d.ControlBytes, d.PTYQueueBytes, d.InitializeMS, d.APIMS, d.RenderMS, d.CommandMS, d.InteractionMS, d.ShutdownMS}
	for i, v := range p {
		if *v == 0 {
			*v = q[i]
		}
		if *v < 1 {
			return c, errors.New("plugin: limits must be positive")
		}
	}
	if c.MessageBytes > 64<<20 || c.ControlQueue > 4096 || c.ControlBytes > 256<<20 || c.PTYQueueBytes > 256<<20 {
		return c, errors.New("plugin: excessive queue/message limit")
	}
	for _, timeout := range []int{c.InitializeMS, c.APIMS, c.RenderMS, c.CommandMS, c.InteractionMS, c.ShutdownMS} {
		if timeout > 86400000 {
			return c, errors.New("plugin: deadline exceeds 24 hours")
		}
	}
	return c, nil
}
func milliseconds(n int) time.Duration { return time.Duration(n) * time.Millisecond }
