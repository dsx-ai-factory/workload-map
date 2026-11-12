// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/workload"
)

func TestSortViews(t *testing.T) {
	now := time.Date(2026, time.November, 12, 10, 0, 0, 0, time.UTC)
	// view builds a row; a zero age means the creation time is missing.
	view := func(kind, namespace, name string, age time.Duration) workload.View {
		v := workload.View{Kind: kind, Namespace: namespace, Name: name, APIVersion: "v1"}
		if age != 0 {
			v.CreatedAt = now.Add(-age)
		}
		return v
	}
	ids := func(views []workload.View) []string {
		out := make([]string, 0, len(views))
		for _, v := range views {
			out = append(out, v.Kind+"/"+v.Namespace+"/"+v.Name)
		}
		return out
	}

	for _, tc := range []struct {
		name  string
		key   sortKey
		views []workload.View
		want  []string
	}{
		{
			name:  "age is newest first",
			key:   sortByAge,
			views: []workload.View{view("JobSet", "ml", "alpha", time.Hour), view("JobSet", "ml", "zebra", 2*time.Minute)},
			want:  []string{"JobSet/ml/zebra", "JobSet/ml/alpha"},
		},
		{
			name: "age puts a missing creation time last",
			key:  sortByAge,
			views: []workload.View{
				view("JobSet", "ml", "unset", 0), view("JobSet", "ml", "old", 5*time.Hour), view("JobSet", "ml", "new", time.Minute),
			},
			want: []string{"JobSet/ml/new", "JobSet/ml/old", "JobSet/ml/unset"},
		},
		{
			name: "equal ages fall back to kind, then name",
			key:  sortByAge,
			views: []workload.View{
				view("Pod", "ml", "debug", time.Hour), view("JobSet", "ml", "preprocess", time.Hour), view("Deployment", "ml", "web", time.Hour),
			},
			want: []string{"Deployment/ml/web", "JobSet/ml/preprocess", "Pod/ml/debug"},
		},
		{
			name:  "an unset key sorts by age",
			key:   "",
			views: []workload.View{view("JobSet", "ml", "alpha", time.Hour), view("JobSet", "ml", "zebra", 2*time.Minute)},
			want:  []string{"JobSet/ml/zebra", "JobSet/ml/alpha"},
		},
		{
			name:  "name is alphabetical whatever the age",
			key:   sortByName,
			views: []workload.View{view("JobSet", "ml", "zebra", 2*time.Minute), view("JobSet", "ml", "alpha", time.Hour)},
			want:  []string{"JobSet/ml/alpha", "JobSet/ml/zebra"},
		},
		{
			name: "name keeps missing creation times in name order",
			key:  sortByName,
			views: []workload.View{
				view("JobSet", "ml", "beta", 0), view("JobSet", "ml", "alpha", time.Hour), view("JobSet", "ml", "gamma", 0),
			},
			want: []string{"JobSet/ml/alpha", "JobSet/ml/beta", "JobSet/ml/gamma"},
		},
		{
			name: "equal names fall back to kind, then namespace",
			key:  sortByName,
			views: []workload.View{
				view("JobSet", "b", "web", time.Minute), view("Deployment", "b", "web", time.Hour), view("Deployment", "a", "web", 0),
			},
			want: []string{"Deployment/a/web", "Deployment/b/web", "JobSet/b/web"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			views := slices.Clone(tc.views)
			sortViews(views, tc.key)
			if got := ids(views); !slices.Equal(got, tc.want) {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

// createdAgo stamps obj with a creation time d before now, and returns it.
func createdAgo(obj *unstructured.Unstructured, d time.Duration) *unstructured.Unstructured {
	obj.SetCreationTimestamp(metav1.NewTime(time.Now().Add(-d)))
	return obj
}

func TestGetSortBy(t *testing.T) {
	objects := func() []*unstructured.Unstructured {
		return []*unstructured.Unstructured{
			createdAgo(jobSet("zebra", 1), 2*time.Minute),
			createdAgo(jobSet("alpha", 1), time.Hour),
			createdAgo(jobSet("mid", 1), 30*time.Minute),
		}
	}
	newestFirst := []string{"zebra", "mid", "alpha"}
	alphabetical := []string{"alpha", "mid", "zebra"}

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"default is newest first", nil, newestFirst},
		{"age is newest first", []string{"--sort-by", "age"}, newestFirst},
		{"name is alphabetical", []string{"--sort-by", "name"}, alphabetical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objs := objects()
			fakeCluster(t, objs[0], objs[1], objs[2])

			out, errOut, code := runGetCmd(t, append([]string{"jobset"}, tc.args...)...)
			if code != 0 {
				t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
			}
			if got := rowNames(out); !slices.Equal(got, tc.want) {
				t.Errorf("table: expected %v, got %v\n%s", tc.want, got, out)
			}

			fakeCluster(t, objs[0], objs[1], objs[2])
			out, errOut, code = runGetCmd(t, append([]string{"jobset", "-o", "json"}, tc.args...)...)
			if code != 0 {
				t.Fatalf("json: expected exit 0, got %d\n%s", code, errOut)
			}
			var list struct {
				Items []workload.View `json:"items"`
			}
			if err := json.Unmarshal([]byte(out), &list); err != nil {
				t.Fatalf("decode json: %v\n%s", err, out)
			}
			var names []string
			for _, item := range list.Items {
				names = append(names, item.Name)
			}
			if !slices.Equal(names, tc.want) {
				t.Errorf("json: expected %v, got %v", tc.want, names)
			}

			fakeCluster(t, objs[0], objs[1], objs[2])
			out, errOut, code = runGetCmd(t, append([]string{"jobset", "-o", "yaml"}, tc.args...)...)
			if code != 0 {
				t.Fatalf("yaml: expected exit 0, got %d\n%s", code, errOut)
			}
			positions := make([]int, 0, len(tc.want))
			for _, name := range tc.want {
				positions = append(positions, strings.Index(out, "name: "+name+"\n"))
			}
			if slices.Contains(positions, -1) || !slices.IsSorted(positions) {
				t.Errorf("yaml: expected rows in order %v\n%s", tc.want, out)
			}
		})
	}
}

func TestGetAllTypesSortByName(t *testing.T) {
	fakeMixedCluster(t, mixedNamespace()...)

	out, errOut, code := runGetCmd(t, "--all-types", "--sort-by", "name")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	want := []string{"debug-shell", "preprocess", "web"}
	if got := rowNames(out); !slices.Equal(got, want) {
		t.Errorf("expected rows %v, got %v\n%s", want, got, out)
	}
}

func TestGetRejectsAnUnknownSortKey(t *testing.T) {
	fakeCluster(t, jobSet("preprocess", 1))

	out, errOut, code := runGetCmd(t, "jobset", "--sort-by", "size")
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "must be one of age, name") {
		t.Errorf("expected the allowed keys in the error, got: %s", errOut)
	}
	if out != "" {
		t.Errorf("expected no rows on a usage error, got: %s", out)
	}
}

func TestCompleteOffersTheSortKeys(t *testing.T) {
	fakeCluster(t)

	if got, want := complete(t, "get", "--sort-by", ""), []string{"age", "name"}; !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}
