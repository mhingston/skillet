package authorization

const (
	ActionCollaborationRead     Action = "collaboration.read"
	ActionCollaborationComment  Action = "collaboration.comment"
	ActionCollaborationWatch    Action = "collaboration.watch"
	ActionCollaborationModerate Action = "collaboration.moderate"
)

func init() {
	supportedActions[ActionCollaborationRead] = struct{}{}
	supportedActions[ActionCollaborationComment] = struct{}{}
	supportedActions[ActionCollaborationWatch] = struct{}{}
	supportedActions[ActionCollaborationModerate] = struct{}{}
}
