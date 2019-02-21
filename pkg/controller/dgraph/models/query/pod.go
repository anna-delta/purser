/*
 * Copyright (c) 2018 VMware Inc. All Rights Reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package query

import (
	"fmt"
	"strconv"

	"github.com/Sirupsen/logrus"
	"github.com/vmware/purser/pkg/controller/dgraph"
	"github.com/vmware/purser/pkg/controller/dgraph/models"
	"github.com/vmware/purser/pkg/controller/utils"
)

// RetrievePodsInteractions returns inbound and outbound interactions of a pod
func RetrievePodsInteractions(name string, isOrphan bool) []byte {
	var query string
	if name == All {
		if isOrphan {
			query = `query {
				pods(func: has(isPod)) {
					name
					outbound: pod {
						name
					}
					inbound: ~pod @filter(has(isPod)) {
						name
					}
				}
			}`
		} else {
			query = `query {
				pods(func: has(isPod)) @filter(has(pod)) {
					name
					outbound: pod {
						name
					}
					inbound: ~pod @filter(has(isPod)) {
						name
					}
				}
			}`
		}
	} else {
		query = `query {
			pods(func: has(isPod)) @filter(eq(name, "` + name + `")) {
				name
				outbound: pod {
					name
				}
				inbound: ~pod @filter(has(isPod)) {
					name
				}
			}
		}`
	}

	result, err := dgraph.ExecuteQueryRaw(query)
	if err != nil {
		logrus.Errorf("Error while retrieving query for pods interactions. Name: (%v), isOrphan: (%v), error: (%v)", name, isOrphan, err)
		return nil
	}
	return result
}

// RetrievePodHierarchy returns hierarchy for a given pod
func RetrievePodHierarchy(name string) JSONDataWrapper {
	if name == All {
		logrus.Errorf("wrong type of query for pod, empty name is given")
		return JSONDataWrapper{}
	}
	query := `query {
		parent(func: has(isPod)) @filter(eq(name, "` + name + `")) {
			name
			type
			children: ~pod @filter(has(isContainer)) {
				name
				type
			}
		}
	}`
	return getJSONDataFromQuery(query)
}

// RetrievePodMetrics returns metrics for a given pod
func RetrievePodMetrics(name string) JSONDataWrapper {
	if name == All {
		logrus.Errorf("wrong type of query for pod, empty name is given")
		return JSONDataWrapper{}
	}
	secondsSinceMonthStart := fmt.Sprintf("%f", utils.GetSecondsSince(utils.GetCurrentMonthStartTime()))
	cpuPriceInFloat64, memoryPriceInFloat64 := getPricePerResourceForPod(name)
	cpuPrice := strconv.FormatFloat(cpuPriceInFloat64, 'f', 11, 64)
	memoryPrice := strconv.FormatFloat(memoryPriceInFloat64, 'f', 11, 64)
	query := `query {
		parent(func: has(isPod)) @filter(eq(name, "` + name + `")) {
			name
			type
			children: ~pod @filter(has(isContainer)) {
				name
				type
				stChild as startTime
				stSecondsChild as math(since(stChild))
				secondsSinceStartChild as math(cond(stSecondsChild > ` + secondsSinceMonthStart + `, ` + secondsSinceMonthStart + `, stSecondsChild))
				etChild as endTime
				isTerminatedChild as count(endTime)
				secondsSinceEndChild as math(cond(isTerminatedChild == 0, 0.0, since(etChild)))
				durationInHoursChild as math((secondsSinceStartChild - secondsSinceEndChild) / 3600)
				cpu: cpu as cpuRequest
				memory: memory as memoryRequest
				cpuCost: math(cpu * durationInHoursChild * ` + cpuPrice + `)
				memoryCost: math(memory * durationInHoursChild * ` + memoryPrice + `)
			}
			cpu: podCpu as cpuRequest
			memory: podMemory as memoryRequest
			storage: pvcStorage as storageRequest
			st as startTime
			stSeconds as math(since(st))
			secondsSinceStart as math(cond(stSeconds > ` + secondsSinceMonthStart + `, ` + secondsSinceMonthStart + `, stSeconds))
			et as endTime
			isTerminated as count(endTime)
			secondsSinceEnd as math(cond(isTerminated == 0, 0.0, since(et)))
			durationInHours as math((secondsSinceStart - secondsSinceEnd) / 3600)
			cpuCost: math(podCpu * durationInHours * ` + cpuPrice + `)
			memoryCost: math(podMemory * durationInHours * ` + memoryPrice + `)
			storageCost: math(pvcStorage * durationInHours * ` + models.DefaultStorageCostPerGBPerHour + `)
		}
	}`
	return getJSONDataFromQuery(query)
}

func getPricePerResourceForPod(name string) (float64, float64) {
	query := `query {
		pod(func: has(isPod)) @filter(eq(name, "` + name + `")) {
			cpuPrice
			memoryPrice
		}
	}`
	type root struct {
		Pods []models.Pod `json:"pod"`
	}
	newRoot := root{}
	err := dgraph.ExecuteQuery(query, &newRoot)
	if err != nil || len(newRoot.Pods) < 1 {
		return models.DefaultCPUCostInFloat64, models.DefaultMemCostInFloat64
	}
	pod := newRoot.Pods[0]
	return pod.CPUPrice, pod.MemoryPrice
}

// RetrievePodsInteractionsForAllLivePodsWithCount returns all pods in the dgraph
func RetrievePodsInteractionsForAllLivePodsWithCount() ([]models.Pod, error) {
	q := `query {
		pods(func: has(isPod)) @filter((NOT has(endTime))) {
			name
			pod {
				name
				count
			}
			cid: ~pod @filter(has(isService)) {
				name
			}
		}
	}`

	type root struct {
		Pods []models.Pod `json:"pods"`
	}
	newRoot := root{}
	err := dgraph.ExecuteQuery(q, &newRoot)
	if err != nil {
		return nil, err
	}
	return newRoot.Pods, nil
}

// RetrievePodsUIDsByLabelsFilter returns pods satisfying the filter conditions for labels (OR logic only)
func RetrievePodsUIDsByLabelsFilter(labels map[string][]string) ([]string, error) {
	labelFilter := createFilterFromListOfLabels(labels)
	q := `query {
		var(func: has(isLabel)) @filter(` + labelFilter + `) {
            podUIDs as ~label @filter(has(isPod)) {
				name
			}
		}
		pods(func: uid(podUIDs)) {
			uid
			name
		}
	}`
	type root struct {
		Pods []models.Pod `json:"pods"`
	}
	newRoot := root{}
	err := dgraph.ExecuteQuery(q, &newRoot)
	if err != nil {
		return nil, err
	}
	return removeDuplicates(newRoot.Pods), nil
}

func removeDuplicates(pods []models.Pod) []string {
	duplicateChecker := make(map[string]bool)
	var podsUIDs []string
	for _, pod := range pods {
		if _, isPresent := duplicateChecker[pod.UID]; !isPresent {
			podsUIDs = append(podsUIDs, pod.UID)
			duplicateChecker[pod.UID] = true
		}
	}
	return podsUIDs
}
