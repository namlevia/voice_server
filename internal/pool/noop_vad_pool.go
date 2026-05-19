package pool

import (
	"fmt"
	"time"
)

type NoopVADPool struct{}

type NoopVADInstance struct {
	id       int
	inUse    bool
	lastUsed int64
}

func NewNoopVADPool() *NoopVADPool {
	return &NoopVADPool{}
}

func (p *NoopVADPool) Initialize() error {
	return nil
}

func (p *NoopVADPool) Get() (VADInstanceInterface, error) {
	return nil, fmt.Errorf("VAD is disabled")
}

func (p *NoopVADPool) Put(instance VADInstanceInterface) {}

func (p *NoopVADPool) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"type":    NOOP_VAD_TYPE,
		"enabled": false,
	}
}

func (p *NoopVADPool) Shutdown() {}

func (i *NoopVADInstance) GetID() int { return i.id }

func (i *NoopVADInstance) GetType() string { return NOOP_VAD_TYPE }

func (i *NoopVADInstance) IsInUse() bool { return i.inUse }

func (i *NoopVADInstance) SetInUse(inUse bool) { i.inUse = inUse }

func (i *NoopVADInstance) GetLastUsed() int64 { return i.lastUsed }

func (i *NoopVADInstance) SetLastUsed(timestamp int64) { i.lastUsed = timestamp }

func (i *NoopVADInstance) Reset() error {
	i.lastUsed = time.Now().Unix()
	return nil
}

func (i *NoopVADInstance) Destroy() error { return nil }

type NoopVADPoolFactory struct{}

func (f *NoopVADPoolFactory) CreatePool(config interface{}) (VADPoolInterface, error) {
	return NewNoopVADPool(), nil
}

func (f *NoopVADPoolFactory) GetSupportedTypes() []string {
	return []string{NOOP_VAD_TYPE}
}
