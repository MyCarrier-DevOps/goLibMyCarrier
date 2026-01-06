package slippy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SlipQueryBuilder constructs dynamic SQL queries based on pipeline configuration.
// It encapsulates all query building logic for routing slips, ensuring consistent
// column ordering across SELECT, INSERT, and UPDATE operations.
type SlipQueryBuilder struct {
	config   *PipelineConfig
	database string
}

// NewSlipQueryBuilder creates a new query builder for the given configuration.
func NewSlipQueryBuilder(config *PipelineConfig, database string) *SlipQueryBuilder {
	if database == "" {
		database = "ci"
	}
	return &SlipQueryBuilder{
		config:   config,
		database: database,
	}
}

// Database returns the configured database name.
func (b *SlipQueryBuilder) Database() string {
	return b.database
}

// BuildSelectQuery builds a SELECT query with dynamic columns.
func (b *SlipQueryBuilder) BuildSelectQuery(whereClause, suffix string) string {
	columns := b.BuildSelectColumns()
	return fmt.Sprintf(`
		SELECT %s
		FROM %s.routing_slips FINAL
		%s
		%s
	`, strings.Join(columns, ", "), b.database, whereClause, suffix)
}

// BuildSelectColumns returns the ordered list of columns to select.
// The order is: core columns, step status columns, aggregate JSON columns.
func (b *SlipQueryBuilder) BuildSelectColumns() []string {
	columns := []string{
		ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, ColumnUpdatedAt, ColumnStatus,
		ColumnStepDetails, ColumnStateHistory,
	}

	// Add step status columns
	for _, step := range b.config.Steps {
		columns = append(columns, b.StepStatusColumn(step.Name))
	}

	// Add aggregate JSON columns
	for _, step := range b.config.Steps {
		if step.Aggregates != "" {
			columns = append(columns, pluralize(step.Aggregates))
		}
	}

	return columns
}

// BuildSelectColumnsWithPrefix returns columns with a table alias prefix.
func (b *SlipQueryBuilder) BuildSelectColumnsWithPrefix(prefix string) string {
	columns := b.BuildSelectColumns()
	prefixed := make([]string, len(columns))
	for i, col := range columns {
		prefixed[i] = prefix + col
	}
	return strings.Join(prefixed, ", ")
}

// BuildInsertQuery builds an INSERT statement for a slip.
func (b *SlipQueryBuilder) BuildInsertQuery(columns, placeholders []string) string {
	return fmt.Sprintf(`
		INSERT INTO %s.routing_slips (%s)
		VALUES (%s)
	`, b.database, strings.Join(columns, ", "), strings.Join(placeholders, ", "))
}

// StepStatusColumn returns the column name for a step's status.
func (b *SlipQueryBuilder) StepStatusColumn(stepName string) string {
	return fmt.Sprintf("%s_status", stepName)
}

// AggregateColumn returns the JSON column name for an aggregate step.
func (b *SlipQueryBuilder) AggregateColumn(aggregateName string) string {
	return pluralize(aggregateName)
}

// BuildFindByCommitsQuery builds a query to find a slip by a list of commits.
func (b *SlipQueryBuilder) BuildFindByCommitsQuery() string {
	selectColumns := b.BuildSelectColumnsWithPrefix("s.")

	return fmt.Sprintf(`
		WITH commits AS (
			SELECT 
				arrayJoin(range(1, length({commits:Array(String)}) + 1)) AS priority,
				{commits:Array(String)}[priority] AS commit_sha
		)
		SELECT 
			%s,
			c.commit_sha AS matched_commit
		FROM %s.routing_slips s FINAL
		INNER JOIN commits c ON s.commit_sha = c.commit_sha
		WHERE s.repository = {repository:String}
		ORDER BY c.priority ASC
		LIMIT 1
	`, selectColumns, b.database)
}

// BuildStepColumnsAndValues builds step status column data for INSERT.
// Returns column names, placeholders, and values in matching order.
func (b *SlipQueryBuilder) BuildStepColumnsAndValues(
	steps map[string]Step,
) (columns, placeholders []string, values []interface{}) {
	for _, step := range b.config.Steps {
		columnName := b.StepStatusColumn(step.Name)
		columns = append(columns, columnName)
		placeholders = append(placeholders, "?")

		status := StepStatusPending
		if stepData, ok := steps[step.Name]; ok {
			status = stepData.Status
		}
		values = append(values, string(status))
	}
	return columns, placeholders, values
}

// BuildAggregateColumnsAndValues builds aggregate JSON column data for INSERT.
// Returns column names, placeholders, and values in matching order.
func (b *SlipQueryBuilder) BuildAggregateColumnsAndValues(
	aggregates map[string][]ComponentStepData,
) (columns, placeholders []string, values []interface{}) {
	for _, step := range b.config.Steps {
		if step.Aggregates == "" {
			continue
		}

		columnName := b.AggregateColumn(step.Aggregates)
		columns = append(columns, columnName)
		placeholders = append(placeholders, "?")

		// Get component data from aggregates map
		var componentData []ComponentStepData
		if aggregates != nil {
			if data, ok := aggregates[columnName]; ok {
				componentData = data
			}
		}

		// Marshal to JSON - empty slice if no data
		if componentData == nil {
			componentData = []ComponentStepData{}
		}
		jsonData, err := json.Marshal(componentData)
		if err != nil {
			// Use empty JSON array on marshal error
			jsonData = []byte("[]")
		}
		values = append(values, string(jsonData))
	}
	return columns, placeholders, values
}

// GetStepCount returns the number of steps in the pipeline.
func (b *SlipQueryBuilder) GetStepCount() int {
	return len(b.config.Steps)
}

// GetAggregateSteps returns all steps that have aggregates.
func (b *SlipQueryBuilder) GetAggregateSteps() []StepConfig {
	return b.config.GetAggregateSteps()
}
