# Specs

## SPEC-001
The azstral system shall parse Go source files into a directed graph of nodes representing packages, files, functions, types, and comments.

## SPEC-002
The azstral system shall extract SPEC, NOTE, and TEST identifiers from Go comments and link them to the code they annotate.

## SPEC-003
The azstral system shall expose the code graph via an HTTP API with endpoints for querying nodes, edges, and spec coverage.

## SPEC-004
The /cmd/hello application shall print "Hello World" to the console.

## SPEC-005
The azstral system shall use go/ast, go/parser, and go/token to build the code graph from source files.

## SPEC-006
The azstral system shall support node types: Package, File, Function, Type, Variable, Comment, and Spec.

## SPEC-007
The azstral system shall support edge types: Contains, Calls, References, Annotates, and Covers.

## SPEC-008
The azstral system shall support expression-level node types: ExprBinary,
ExprUnary, ExprIdent, ExprSelector, ExprIndex, ExprLiteral, ExprComposite,
ExprTypeAssert, and ExprFunc. These nodes are children of statement nodes and
represent the internal expression tree of Go statements. They are created by
the parser but ignored by the code generator (codegen uses src metadata
strings from statement nodes for reconstruction).
