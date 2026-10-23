package driver

// EngineRunning reports host-side liveness without reading the event queue or
// scheduling any simulated work. Diagnostics may call it from another goroutine.
func (d *Driver) EngineRunning() bool {
	d.engineRunningMutex.Lock()
	defer d.engineRunningMutex.Unlock()
	return d.engineRunning
}
