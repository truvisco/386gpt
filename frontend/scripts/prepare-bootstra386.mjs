import { copyFile, mkdir } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'

const source = resolve('node_modules/bootstra.386/fonts/Px437_IBM_EGA8.otf')
const target = resolve('node_modules/bootstra.386/v5.3.1/dist/fonts/Px437_IBM_EGA8.otf')

await mkdir(dirname(target), { recursive: true })
await copyFile(source, target)
