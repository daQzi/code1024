package generation

import "errors"

var ErrTaskNotFound = errors.New("generation task not found")
var ErrLeaseLost = errors.New("generation task lease lost")
var ErrOrigin = errors.New("invalid origin task snapshot")
