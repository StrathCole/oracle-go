package eventstream

// VotingPeriod contains information about a voting period that has started
type VotingPeriod struct {
	// Height is the block height at which this voting period starts
	Height uint64
	// Period is the sequential period number (Height / VotePeriodBlocks)
	Period uint64
}

// Params contains oracle parameters needed for price feeding
type Params struct {
	// VotePeriod is the number of blocks per voting period
	VotePeriod uint64
	// Whitelist is the list of denoms that can be voted on
	Whitelist []string
}

// EventStream defines the interface for receiving blockchain events
type EventStream interface {
	// VotingPeriodStarted returns a channel that signals when a new voting period begins
	VotingPeriodStarted() <-chan VotingPeriod

	// ParamsUpdate returns a channel that signals when oracle params are updated
	ParamsUpdate() <-chan Params

	// Close shuts down the event stream
	Close()
}
