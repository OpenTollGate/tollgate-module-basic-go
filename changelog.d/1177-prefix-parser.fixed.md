- **Prefix-file parsing:** setup rejects internal whitespace instead of deleting
  it, preserves boundary trimming around optional decoration, and processes a
  final line without a newline. A shared acceptance fixture exercises both the
  Go and shell readers ([#1177](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1177)).
