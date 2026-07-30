// Vitest global setup — see vite.config.ts's test.setupFiles. Ensures any
// test that stubs fetch/globals via vi.stubGlobal doesn't leak into the
// next test file.
import { afterEach, vi } from 'vitest'

afterEach(() => {
  vi.unstubAllGlobals()
})
