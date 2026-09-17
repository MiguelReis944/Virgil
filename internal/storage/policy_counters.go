package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"
)

type PolicyReserve struct {
	RunID, ReservationID                                                                        string
	StartedAt                                                                                   time.Time
	InputTokens, OutputTokens, ToolCalls                                                        int64
	CostUSD                                                                                     string
	MaxCalls, MaxInputTokens, MaxOutputTokens, MaxTotalTokens, MaxToolCalls, MaxDurationSeconds int64
	MaxCostUSD                                                                                  string
}

type PolicyReserveResult struct {
	Reason, Policy     string
	Attempt, Threshold int64
}

type PolicyOutcome struct {
	RunID, ReservationID                 string
	InputTokens, OutputTokens, ToolCalls int64  // -1 means unavailable; keep reserved estimate.
	CostUSD                              string // empty means unavailable; keep reserved estimate.
}

func checkedAdd(values ...int64) (int64, error) {
	var total int64
	for _, value := range values {
		if value < 0 || total > math.MaxInt64-value {
			return 0, errors.New("policy counter overflow or negative usage")
		}
		total += value
	}
	return total, nil
}

func policyDecimal(value string) (*big.Rat, error) {
	if value == "" {
		return new(big.Rat), nil
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok || r.Sign() < 0 {
		return nil, errors.New("invalid policy cost")
	}
	return r, nil
}

func (j *Journal) ReservePolicy(ctx context.Context, r PolicyReserve) (PolicyReserveResult, error) {
	if r.RunID == "" || r.ReservationID == "" {
		return PolicyReserveResult{}, errors.New("run and reservation IDs are required")
	}
	if _, err := checkedAdd(r.InputTokens, r.OutputTokens); err != nil {
		return PolicyReserveResult{}, err
	}
	if r.ToolCalls < 0 {
		return PolicyReserveResult{}, errors.New("negative tool calls")
	}
	estCost, err := policyDecimal(r.CostUSD)
	if err != nil {
		return PolicyReserveResult{}, err
	}
	capCost, err := policyDecimal(r.MaxCostUSD)
	if err != nil {
		return PolicyReserveResult{}, err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return PolicyReserveResult{}, err
	}
	defer tx.Rollback()
	started := r.StartedAt.UnixNano()
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO policy_runs(run_id,started_at_unix_ns) VALUES (?,?)", r.RunID, started); err != nil {
		return PolicyReserveResult{}, err
	}
	var first, calls, input, output, tools int64
	var costText string
	if err = tx.QueryRowContext(ctx, "SELECT started_at_unix_ns,calls,input_tokens,output_tokens,tool_calls,cost_usd FROM policy_runs WHERE run_id=?", r.RunID).Scan(&first, &calls, &input, &output, &tools, &costText); err != nil {
		return PolicyReserveResult{}, err
	}
	currentCost, err := policyDecimal(costText)
	if err != nil {
		return PolicyReserveResult{}, err
	}
	nextInput, err := checkedAdd(input, r.InputTokens)
	if err != nil {
		return PolicyReserveResult{}, err
	}
	nextOutput, err := checkedAdd(output, r.OutputTokens)
	if err != nil {
		return PolicyReserveResult{}, err
	}
	nextTools, err := checkedAdd(tools, r.ToolCalls)
	if err != nil {
		return PolicyReserveResult{}, err
	}
	nextTotal, err := checkedAdd(nextInput, nextOutput)
	if err != nil {
		return PolicyReserveResult{}, err
	}
	if calls == math.MaxInt64 {
		return PolicyReserveResult{}, errors.New("policy call counter overflow")
	}
	block := func(reason, policy string, attempt, threshold int64) (PolicyReserveResult, error) {
		return PolicyReserveResult{Reason: reason, Policy: policy, Attempt: attempt, Threshold: threshold}, nil
	}
	if r.MaxDurationSeconds > 0 && time.Since(time.Unix(0, first)) > time.Duration(r.MaxDurationSeconds)*time.Second {
		return block("duration_limit", "max_duration_seconds", calls+1, r.MaxDurationSeconds)
	}
	if r.MaxCalls > 0 && calls >= r.MaxCalls {
		return block("call_limit", "max_requests_per_run", calls+1, r.MaxCalls)
	}
	if r.MaxInputTokens > 0 && nextInput > r.MaxInputTokens {
		return block("input_token_limit", "max_input_tokens_per_run", calls+1, r.MaxInputTokens)
	}
	if r.MaxOutputTokens > 0 && nextOutput > r.MaxOutputTokens {
		return block("output_token_limit", "max_output_tokens_per_run", calls+1, r.MaxOutputTokens)
	}
	if r.MaxTotalTokens > 0 && nextTotal > r.MaxTotalTokens {
		return block("total_token_limit", "max_total_tokens_per_run", calls+1, r.MaxTotalTokens)
	}
	if r.MaxToolCalls > 0 && nextTools > r.MaxToolCalls {
		return block("tool_call_limit", "max_tool_calls_per_run", calls+1, r.MaxToolCalls)
	}
	nextCost := new(big.Rat).Add(currentCost, estCost)
	if r.MaxCostUSD != "" && nextCost.Cmp(capCost) > 0 {
		// Decision.Threshold is an integer; the exact decimal cap remains in configuration.
		return block("cost_limit", "max_cost_per_run_usd", calls+1, 0)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE policy_runs SET calls=calls+1,input_tokens=input_tokens+?,output_tokens=output_tokens+?,tool_calls=tool_calls+?,cost_usd=? WHERE run_id=?", r.InputTokens, r.OutputTokens, r.ToolCalls, nextCost.RatString(), r.RunID); err != nil {
		return PolicyReserveResult{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO policy_reservations(reservation_id,run_id,input_tokens,output_tokens,tool_calls,cost_usd) VALUES (?,?,?,?,?,?)", r.ReservationID, r.RunID, r.InputTokens, r.OutputTokens, r.ToolCalls, estCost.RatString()); err != nil {
		return PolicyReserveResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return PolicyReserveResult{}, err
	}
	return PolicyReserveResult{Attempt: calls + 1}, nil
}

func (j *Journal) ReconcilePolicy(ctx context.Context, o PolicyOutcome) error {
	if o.ReservationID == "" || o.RunID == "" {
		return errors.New("run and reservation IDs are required")
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldIn, oldOut, oldTools int64
	var oldCostText string
	var reconciled bool
	err = tx.QueryRowContext(ctx, "SELECT input_tokens,output_tokens,tool_calls,cost_usd,reconciled FROM policy_reservations WHERE reservation_id=? AND run_id=?", o.ReservationID, o.RunID).Scan(&oldIn, &oldOut, &oldTools, &oldCostText, &reconciled)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("unknown policy reservation: %w", err)
	}
	if err != nil {
		return err
	}
	if reconciled {
		return nil
	}
	var currentCostText string
	var currentIn, currentOut, currentTools int64
	if err = tx.QueryRowContext(ctx, "SELECT input_tokens,output_tokens,tool_calls,cost_usd FROM policy_runs WHERE run_id=?", o.RunID).Scan(&currentIn, &currentOut, &currentTools, &currentCostText); err != nil {
		return err
	}
	currentCost, err := policyDecimal(currentCostText)
	if err != nil {
		return err
	}
	oldCost, err := policyDecimal(oldCostText)
	if err != nil {
		return err
	}
	actualCost := oldCost
	if o.CostUSD != "" {
		actualCost, err = policyDecimal(o.CostUSD)
		if err != nil {
			return err
		}
	}
	input, output, tools := oldIn, oldOut, oldTools
	if o.InputTokens >= 0 {
		input = o.InputTokens
	}
	if o.OutputTokens >= 0 {
		output = o.OutputTokens
	}
	if o.ToolCalls >= 0 {
		tools = o.ToolCalls
	}
	if _, err = checkedAdd(input, output); err != nil {
		return err
	}
	if tools < 0 {
		return errors.New("negative tool calls")
	}
	if currentIn < oldIn || currentOut < oldOut || currentTools < oldTools {
		return errors.New("policy counter underflow")
	}
	nextIn, err := checkedAdd(currentIn-oldIn, input)
	if err != nil {
		return err
	}
	nextOut, err := checkedAdd(currentOut-oldOut, output)
	if err != nil {
		return err
	}
	nextTools, err := checkedAdd(currentTools-oldTools, tools)
	if err != nil {
		return err
	}
	if _, err = checkedAdd(nextIn, nextOut); err != nil {
		return err
	}
	newCost := new(big.Rat).Add(new(big.Rat).Sub(currentCost, oldCost), actualCost)
	if _, err = tx.ExecContext(ctx, "UPDATE policy_runs SET input_tokens=?,output_tokens=?,tool_calls=?,cost_usd=? WHERE run_id=?", nextIn, nextOut, nextTools, newCost.RatString(), o.RunID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE policy_reservations SET reconciled=1,input_tokens=?,output_tokens=?,tool_calls=?,cost_usd=? WHERE reservation_id=?", input, output, tools, actualCost.RatString(), o.ReservationID); err != nil {
		return err
	}
	return tx.Commit()
}
