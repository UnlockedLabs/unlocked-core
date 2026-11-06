package programs

// The order_by values each list query accepts. A value outside its list falls
// back to the query's default order, so request text never reaches ORDER BY.
var (
	programOrderBy          = []string{"name", "programs.created_at"}
	programOverviewOrderBy  = []string{"programs.name", "mr.total_active_enrollments", "completion_rate", "programs.created_at"}
	classDetailsOrderBy     = []string{"ps.start_dt", "ps.created_at"}
	classOutcomesOrderBy    = []string{"months.month", "month", "completions", "drops"}
	classHistoryOrderBy     = []string{"created_at"}
	attendanceOrderBy       = []string{"program_class_event_attendance.created_at"}
	attendanceRosterOrderBy = []string{"name_first", "name_last"}
	userProgramOrderBy      = []string{"pce.created_at", "start_dt"}
	enrollmentOrderBy       = []string{"name_last", "doc_id", "start_dt", "pse.created_at"}
)
