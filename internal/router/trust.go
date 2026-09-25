package router

// TrustSource reports channels whose authenticity probes failed.
type TrustSource interface {
	Suspect(channelID string) (bool, string)
}

// ChannelBindings lists how each model served by channelID is reached. Not
// implemented yet.
func (r Resolver) ChannelBindings(channelID string) []Decision { return nil }
