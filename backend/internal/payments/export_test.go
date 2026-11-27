package payments

// Finalize exposes finalize to the external tests, which cannot be internal
// because they use fakegw, which imports this package.
var Finalize = (*Service).finalize
