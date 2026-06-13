package cli

import (
	"errors"

	"github.com/tamnd/nlab-cli/nlab"
)

func isNotFound(err error) bool {
	return errors.Is(err, nlab.ErrNotFound)
}

func mapFetchErr(err error) error {
	if err == nil {
		return nil
	}
	if isNotFound(err) {
		return codeError(exitNoData, err)
	}
	return codeError(exitError, err)
}
