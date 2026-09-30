// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package recorder

import (
	"fmt"
	"slices"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// validateObservedOrder checks that observed is a legal walk of the journey:
//
//  1. Consecutive duplicate observations collapse to one visit; an empty
//     observation list always fails.
//  2. The last observed state must equal the expected terminal.
//  3. Undefined visits before the terminal are dropped from the walk: a frame
//     no predicate matched is a coverage gap, not a transition the workload
//     made. A run that ends Undefined still fails rule 2.
//  4. The collapsed walk must be the journey with zero or more absent-allowed
//     steps removed, in order, nothing extra. A step is allowed to be absent
//     when it is Optional, or when its state already appears earlier in the
//     journey: the recorder collapses duplicates, so a declared revisit can
//     merge into the earlier visit and can never be demanded.
//
// Example: journey [Init, Running, Init, Completed] accepts observed
// [Init, Running, Completed], the declared dip back to Init may be missed;
// journey [Init, Running, Completed] rejects it, the dip was never declared.
func validateObservedOrder(journey []journeyStep, observed []kartav1alpha1.ResourceStatus, expectedTerminal kartav1alpha1.ResourceStatus) error {
	journeyStates := make([]kartav1alpha1.ResourceStatus, len(journey))
	for i, step := range journey {
		journeyStates[i] = step.State
	}
	if len(observed) == 0 {
		return fmt.Errorf("no states observed (journey %v)", journeyStates)
	}

	visits := visitsOf(observed)
	if lastVisit := visits[len(visits)-1]; lastVisit != expectedTerminal {
		return fmt.Errorf("last observed state is %q, expected terminal %q (journey %v, observed %v)",
			lastVisit, expectedTerminal, journeyStates, visits)
	}
	visits = visitsOf(slices.DeleteFunc(visits, func(state kartav1alpha1.ResourceStatus) bool {
		return state == kartav1alpha1.UndefinedStatus
	}))

	// Match the journey against the visits, in order: every journey step either matches the next
	// unmatched visit, or must be allowed to be absent from the walk.
	nextUnmatchedVisit := 0
	for stepIndex, step := range journey {
		stepMatchesNextVisit := nextUnmatchedVisit < len(visits) && visits[nextUnmatchedVisit] == step.State
		stateDeclaredEarlier := slices.Contains(journeyStates[:stepIndex], step.State)

		switch {
		case stepMatchesNextVisit:
			nextUnmatchedVisit++
		case step.Optional || stateDeclaredEarlier:
			// Allowed to be absent: a declared revisit merges into the earlier visit.
		default:
			return fmt.Errorf("required state %q missing or out of order (journey %v, observed %v)",
				step.State, journeyStates, visits)
		}
	}
	if nextUnmatchedVisit < len(visits) {
		return fmt.Errorf("observed state %q is not part of the journey here (journey %v, observed %v)",
			visits[nextUnmatchedVisit], journeyStates, visits)
	}
	return nil
}

func visitsOf(observed []kartav1alpha1.ResourceStatus) []kartav1alpha1.ResourceStatus {
	visits := make([]kartav1alpha1.ResourceStatus, 0, len(observed))
	for _, state := range observed {
		if len(visits) == 0 || visits[len(visits)-1] != state {
			visits = append(visits, state)
		}
	}
	return visits
}
