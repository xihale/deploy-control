/** @type {import('next').NextConfig} */

// 调试时设置 DEV_API_ORIGIN（如 http://127.0.0.1:8790），
// dev server 会把 /api/* 和 /healthz 同源转发到后端，浏览器无需 CORS。
// 设置后不再是静态导出模式，仅用于 `next dev`。
// 不设置时保持 output: 'export'，产物输出到 out/。
const apiOrigin = process.env.DEV_API_ORIGIN ?? ''

const staticExport = apiOrigin === ''

const nextConfig = {
  allowedDevOrigins: ['127.0.0.1', 'localhost'],
  agentRules: false,
  ...(staticExport ? { output: 'export' } : {}),
  trailingSlash: true,
  images: {
    unoptimized: true,
  },
  ...(apiOrigin === ''
    ? {}
    : {
        async rewrites() {
          return [
            { source: '/api/:path*', destination: `${apiOrigin}/api/:path*` },
            { source: '/healthz', destination: `${apiOrigin}/healthz` },
          ]
        },
      }),
}

export default nextConfig
