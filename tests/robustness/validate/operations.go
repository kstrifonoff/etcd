// Copyright 2023 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package validate

import (
	"errors"
	"fmt"
	"time"

	"github.com/anishathalye/porcupine"
	"github.com/google/go-cmp/cmp"
	"go.uber.org/zap"

	"go.etcd.io/etcd/tests/v3/robustness/model"
)

var (
	errRespNotMatched         = errors.New("response didn't match expected")
	errFutureRevRespRequested = errors.New("request about a future rev with response")
)

type Results struct {
	Info         porcupine.LinearizationInfo
	Model        porcupine.Model
	Linearizable porcupine.CheckResult
	Lg           *zap.Logger // TODO: Remove logger from struct and instead of making it an argument for Visualize
}

func (r Results) Visualize(path string) error {
	r.Lg.Info("Saving visualization", zap.String("path", path))
	err := porcupine.VisualizePath(r.Model, r.Info, path)
	if err != nil {
		return fmt.Errorf("failed to visualize, err: %w", err)
	}
	return nil
}

func validateLinearizableOperationsAndVisualize(
	lg *zap.Logger,
	operations []porcupine.Operation,
	timeout time.Duration,
) (results Results) {
	lg.Info("Validating linearizable operations", zap.Duration("timeout", timeout))
	start := time.Now()
	result, info := porcupine.CheckOperationsVerbose(model.NonDeterministicModel, operations, timeout)

	switch result {
	case porcupine.Illegal:
		lg.Error("Linearization failed", zap.Duration("duration", time.Since(start)))
	case porcupine.Unknown:
		lg.Error("Linearization has timed out", zap.Duration("duration", time.Since(start)))
	case porcupine.Ok:
		lg.Info("Linearization success", zap.Duration("duration", time.Since(start)))
	default:
		panic(fmt.Sprintf("Unknown Linearization result %s", result))
	}
	return Results{
		Info:         info,
		Model:        model.NonDeterministicModel,
		Linearizable: result,
		Lg:           lg,
	}
}

func validateSerializableOperations(lg *zap.Logger, operations []porcupine.Operation, replay *model.EtcdReplay) (lastErr error) {
	lg.Info("Validating serializable operations")
	for _, read := range operations {
		request := read.Input.(model.EtcdRequest)
		response := read.Output.(model.MaybeEtcdResponse)
		err := validateSerializableRead(lg, replay, request, response)
		if err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func validateSerializableRead(lg *zap.Logger, replay *model.EtcdReplay, request model.EtcdRequest, response model.MaybeEtcdResponse) error {
	if response.Persisted || response.Error != "" {
		return nil
	}
	state, err := replay.StateForRevision(request.Range.Revision)
	if err != nil {
		if response.Error == model.ErrEtcdFutureRev.Error() {
			return nil
		}
		lg.Error("Failed validating serializable operation", zap.Any("request", request), zap.Any("response", response))
		return errFutureRevRespRequested
	}

	_, expectResp := state.Step(request)

	if diff := cmp.Diff(response.EtcdResponse.Range, expectResp.Range); diff != "" {
		lg.Error("Failed validating serializable operation", zap.Any("request", request), zap.String("diff", diff))
		return errRespNotMatched
	}
	return nil
}
