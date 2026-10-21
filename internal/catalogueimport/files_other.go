//go:build !unix

package catalogueimport

import (
	"errors"
	"os"
)

func openRegular(*os.Root, string) (*os.File, error) {
	return nil, errors.New("catalogue_operator_cli_requires_unix")
}
