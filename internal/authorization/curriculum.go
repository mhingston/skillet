package authorization

const (
	ActionCurriculumRead           Action = "curriculum.read"
	ActionCurriculumRecordGap      Action = "curriculum.record_gap"
	ActionCurriculumPropose        Action = "curriculum.propose"
	ActionCurriculumReview         Action = "curriculum.review"
	ActionCurriculumEvolveSuite    Action = "curriculum.evolve_suite"
	ActionCurriculumPrepareHandoff Action = "curriculum.prepare_handoff"
)

func init() {
	supportedActions[ActionCurriculumRead] = struct{}{}
	supportedActions[ActionCurriculumRecordGap] = struct{}{}
	supportedActions[ActionCurriculumPropose] = struct{}{}
	supportedActions[ActionCurriculumReview] = struct{}{}
	supportedActions[ActionCurriculumEvolveSuite] = struct{}{}
	supportedActions[ActionCurriculumPrepareHandoff] = struct{}{}
}
