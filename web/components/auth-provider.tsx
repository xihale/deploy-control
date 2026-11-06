'use client'

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react'
import { SWRConfig } from 'swr'
import { ApiError, DEMO_TOKEN } from '@/lib/api'

const STORAGE_KEY = 'deploy-console:token'

interface AuthState {
  token: string | null
  ready: boolean
  isDemo: boolean
  login: (token: string, remember: boolean) => void
  logout: () => void
}

const AuthContext = createContext<AuthState | null>(null)

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [token, setToken] = useState<string | null>(null)
  const [ready, setReady] = useState(false)

  useEffect(() => {
    setToken(
      sessionStorage.getItem(STORAGE_KEY) ?? localStorage.getItem(STORAGE_KEY),
    )
    setReady(true)
  }, [])

  const login = useCallback((value: string, remember: boolean) => {
    sessionStorage.removeItem(STORAGE_KEY)
    localStorage.removeItem(STORAGE_KEY)
    ;(remember ? localStorage : sessionStorage).setItem(STORAGE_KEY, value)
    setToken(value)
  }, [])

  const logout = useCallback(() => {
    sessionStorage.removeItem(STORAGE_KEY)
    localStorage.removeItem(STORAGE_KEY)
    setToken(null)
  }, [])

  const value = useMemo(
    () => ({ token, ready, isDemo: token === DEMO_TOKEN, login, logout }),
    [token, ready, login, logout],
  )

  return (
    <AuthContext.Provider value={value}>
      <SWRConfig
        value={{
          revalidateOnFocus: true,
          shouldRetryOnError: false,
          onError: (err) => {
            if (err instanceof ApiError && err.status === 401) logout()
          },
        }}
      >
        {children}
      </SWRConfig>
    </AuthContext.Provider>
  )
}
