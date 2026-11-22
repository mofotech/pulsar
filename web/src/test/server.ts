import { setupServer } from 'msw/node'
import { http, HttpResponse, type JsonBodyType } from 'msw'

/** Shared MSW server; tests register handlers with server.use(...). */
export const server = setupServer()

/** The controller's response envelope: {"data": ..., "meta": {...}}. */
export const ok = (data: JsonBodyType, status = 200) =>
  HttpResponse.json({ data, meta: { request_id: 'test' } }, { status })

export const apiError = (status: number, code: string, message: string) =>
  HttpResponse.json({ error: { code, message }, meta: { request_id: 'test' } }, { status })

export { http, HttpResponse }
