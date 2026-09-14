package authorization

const (
	ActionProposalRead    Action = "proposal.read"
	ActionProposalPrepare Action = "proposal.prepare"
	ActionProposalAttach  Action = "proposal.attach"
	ActionProposalReject  Action = "proposal.reject"
)

func init() {
	supportedActions[ActionProposalRead] = struct{}{}
	supportedActions[ActionProposalPrepare] = struct{}{}
	supportedActions[ActionProposalAttach] = struct{}{}
	supportedActions[ActionProposalReject] = struct{}{}
}
