package authorization

const (
	ActionImproverRead              Action = "improver.read"
	ActionImproverRecordProvenance  Action = "improver.record_provenance"
	ActionImproverConfigureMetaEval Action = "improver.configure_meta_eval"
	ActionImproverEvaluate          Action = "improver.evaluate"
)

func init() {
	supportedActions[ActionImproverRead] = struct{}{}
	supportedActions[ActionImproverRecordProvenance] = struct{}{}
	supportedActions[ActionImproverConfigureMetaEval] = struct{}{}
	supportedActions[ActionImproverEvaluate] = struct{}{}
}
