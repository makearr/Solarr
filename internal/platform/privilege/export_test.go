package privilege

// ChownTree exposes chownTree, which Apply reaches only as root, to the tests.
var ChownTree = chownTree
