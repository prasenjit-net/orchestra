import { loader } from '@monaco-editor/react'
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api.js'
import 'monaco-editor/esm/vs/basic-languages/ini/ini.contribution'
import 'monaco-editor/esm/vs/basic-languages/markdown/markdown.contribution'
import 'monaco-editor/esm/vs/basic-languages/python/python.contribution'
import editorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'

type MonacoEnvironment = {
  getWorker: (_moduleID: string, label: string) => Worker
}

const monacoGlobal = self as typeof globalThis & {
  MonacoEnvironment?: MonacoEnvironment
}

monacoGlobal.MonacoEnvironment = {
  getWorker() {
    return new editorWorker()
  },
}

loader.config({ monaco })
