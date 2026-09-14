package authorization

const (
	ActionExperimentRead         Action = "experiment.read"
	ActionExperimentCreate       Action = "experiment.create"
	ActionExperimentSubmitResult Action = "experiment.submit_result"
	ActionExperimentCancel       Action = "experiment.cancel"
)

func init() {
	supportedActions[ActionExperimentRead] = struct{}{}
	supportedActions[ActionExperimentCreate] = struct{}{}
	supportedActions[ActionExperimentSubmitResult] = struct{}{}
	supportedActions[ActionExperimentCancel] = struct{}{}
}
