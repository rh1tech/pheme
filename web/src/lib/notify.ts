// Small wrappers around Mantine notifications so call sites stay terse and
// consistent. Pass already-translated strings.
import { notifications } from '@mantine/notifications'

/** Show a green success toast. */
export function notifySuccess(message: string): void {
  notifications.show({ color: 'green', message })
}

/** Show a red error toast. When err is provided it is appended as ": <error>". */
export function notifyError(message: string, err?: unknown): void {
  const text = err === undefined ? message : `${message}: ${errorText(err)}`
  notifications.show({ color: 'red', message: text })
}

function errorText(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}
