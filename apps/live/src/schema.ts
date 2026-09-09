import StarterKit from "@tiptap/starter-kit";
import Image from "@tiptap/extension-image";
import { TableKit } from "@tiptap/extension-table";
import TaskList from "@tiptap/extension-task-list";
import TaskItem from "@tiptap/extension-task-item";
import { originalEditorBlocks } from "@myjira/editor-schema";

export const extensions = [StarterKit.configure({ undoRedo: false }), Image.configure({ allowBase64: false }), TableKit, TaskList, TaskItem.configure({ nested: true }), ...originalEditorBlocks];
