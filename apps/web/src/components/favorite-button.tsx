import { observer } from "mobx-react-lite";
import { Star } from "lucide-react";
import { appStore } from "../stores/app-store";
import { api, workspacePath } from "../lib/api";
import { useFavorites } from "../lib/bookmarks";
import { useMutation, useScope } from "../lib/hooks";
import { Button } from "./ui";

export const FavoriteButton = observer(function FavoriteButton({
  entityType,
  entityId,
}: {
  entityType: string;
  entityId: string;
}) {
  const { workspace } = useScope();
  const favorites = useFavorites();
  const mutation = useMutation();
  const favorite = favorites.data?.find(
    (item) => item.entity_type === entityType && item.entity_id === entityId,
  );
  const t = appStore.t;
  return (
    <Button
      size="icon"
      variant="ghost"
      disabled={favorites.loading}
      busy={mutation.busy}
      aria-label={
        favorite
          ? t("取消收藏", "Remove favorite")
          : t("添加收藏", "Add favorite")
      }
      aria-pressed={!!favorite}
      title={mutation.error || undefined}
      onClick={() => {
        mutation.execute(async () => {
          try {
            if (favorite)
              await api.delete(
                `${workspacePath(workspace.id)}/favorites/${favorite.id}`,
              );
            else
              await api.post(`${workspacePath(workspace.id)}/favorites`, {
                entity_type: entityType,
                entity_id: entityId,
                position:
                  Math.max(
                    0,
                    ...(favorites.data ?? []).map((item) => item.position),
                  ) + 1000,
              });
          } catch (cause) {
            appStore.notify(
              cause instanceof Error
                ? cause.message
                : t("收藏更新失败", "Favorite update failed"),
              "error",
            );
            throw cause;
          }
        });
      }}
    >
      <Star size={15} fill={favorite ? "currentColor" : "none"} />
    </Button>
  );
});
