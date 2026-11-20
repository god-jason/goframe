package csrf

// 本文件记录 csrf 包剩余未覆盖语句均为逻辑不可达的防御分支，无法通过测试触发：
//
// 1. csrf.go New 中 rand.Read 的错误返回（"generate secret"）；
// 2. csrf.go Token 中 rand.Read 的错误返回（"generate token"）；
// 3. boot.go Startup 中 New 的错误返回。
//
// 原因：Go 1.24+ 标准库 crypto/rand.Read 在底层熵源出错时会直接调用 fatal 终止进程，
// 永不向调用方返回 error；自定义 rand.Reader 返回错误同样触发 fatal，无法在测试中注入。
// 因此上述分支在当前 Go 版本下为死代码，包覆盖率上限为 77/80 = 96.2%。
