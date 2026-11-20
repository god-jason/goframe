package imagex

// 本文件记录 imagex 包剩余未覆盖语句均为逻辑不可达的防御分支，无法通过测试触发：
//
// 1. Fit 中 math.IsInf(ratio, 1) 的兜底赋值：
//    函数入口已拦截 maxWidth<=0 且 maxHeight<=0 的场景，只要任一维度为正，
//    ratio 必然被有限的 float64 比值收敛，不可能保持 +Inf；
// 2. Process 中 Encode 的错误返回：
//    format 经归一化后只会是 "jpeg" 或 "png"（gif/webp 已转 png），
//    编码目标为内存 bytes.Buffer 且图像恒为合法 RGBA，标准库编码器不会返回错误。
//
// 因此包覆盖率上限为 108/110 = 98.2%。
