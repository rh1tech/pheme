import { useEffect, useState } from "react";
import {
  Avatar,
  Button,
  Container,
  Divider,
  FileButton,
  Group,
  Radio,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { IconUpload, IconTrash } from "@tabler/icons-react";
import { useTranslation } from "react-i18next";
import { api, imageUrl } from "../lib/api";
import { notifyError, notifySuccess } from "../lib/notify";
import { ApiError } from "../lib/api";
import { APP_VERSION, BUILD_ID } from "../lib/version";
import type { NotificationPrivacy, User } from "../lib/types";
import { CardListSkeleton } from "../components/Skeletons";

export function ProfilePage() {
  const { t } = useTranslation();
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [bio, setBio] = useState("");
  const [phone, setPhone] = useState("");
  const [website, setWebsite] = useState("");
  // Held as the enum the server speaks, not as a pair of booleans: the three options are mutually
  // exclusive and ordered by how much they reveal, and any boolean decomposition of that has an
  // unrepresentable fourth state somebody eventually has to handle.
  const [privacy, setPrivacy] = useState<NotificationPrivacy>("sender");
  const [saving, setSaving] = useState(false);
  const [uploading, setUploading] = useState(false);

  function fill(u: User) {
    setUser(u);
    setUsername(u.username ?? "");
    setDisplayName(u.displayName ?? "");
    setBio(u.bio ?? "");
    setPhone(u.phone ?? "");
    setWebsite(u.website ?? "");
    // Absent means the account predates the setting, which behaves as 'sender'.
    setPrivacy(u.notificationPrivacy ?? "sender");
  }

  useEffect(() => {
    let active = true;
    api
      .getMe()
      .then((u) => active && fill(u))
      .catch((e) => active && notifyError(t("profile.loadFailed"), e))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function save() {
    setSaving(true);
    try {
      const updated = await api.updateMe({
        username: username.trim(),
        displayName: displayName.trim(),
        bio: bio.trim(),
        phone: phone.trim(),
        website: website.trim(),
        notificationPrivacy: privacy,
      });
      fill(updated);
      notifySuccess(t("profile.saved"));
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        notifyError(t("profile.usernameTaken"));
      } else {
        notifyError(t("profile.saveFailed"), e);
      }
    } finally {
      setSaving(false);
    }
  }

  async function upload(file: File | null) {
    if (!file) return;
    setUploading(true);
    try {
      const updated = await api.uploadAvatar(file);
      fill(updated);
      notifySuccess(t("profile.avatarUpdated"));
    } catch (e) {
      notifyError(t("profile.avatarFailed"), e);
    } finally {
      setUploading(false);
    }
  }

  async function removeAvatar() {
    setUploading(true);
    try {
      const updated = await api.deleteAvatar();
      fill(updated);
      notifySuccess(t("profile.avatarRemoved"));
    } catch (e) {
      notifyError(t("profile.avatarFailed"), e);
    } finally {
      setUploading(false);
    }
  }

  return (
    <Container size="sm">
      <Stack gap="lg">
        <div>
          <Title order={3}>{t("profile.title")}</Title>
          <Text c="dimmed" size="sm">
            {t("profile.subtitle")}
          </Text>
        </div>

        {loading && <CardListSkeleton rows={1} />}

        {user && (
          <>
            <Group align="center" gap="lg">
              <Avatar
                src={user.avatarId ? imageUrl(user.avatarId) : undefined}
                size={88}
                radius="50%"
                color="iris"
              >
                {(user.displayName || user.username || user.email)
                  .slice(0, 2)
                  .toUpperCase()}
              </Avatar>
              <Stack gap="xs">
                <Group gap="xs">
                  <FileButton
                    onChange={upload}
                    accept="image/png,image/jpeg,image/webp"
                  >
                    {(props) => (
                      <Button
                        {...props}
                        size="xs"
                        variant="light"
                        leftSection={<IconUpload size={14} />}
                        loading={uploading}
                      >
                        {t("profile.uploadAvatar")}
                      </Button>
                    )}
                  </FileButton>
                  {user.avatarId && (
                    <Button
                      size="xs"
                      variant="subtle"
                      color="red"
                      leftSection={<IconTrash size={14} />}
                      onClick={removeAvatar}
                      loading={uploading}
                    >
                      {t("profile.removeAvatar")}
                    </Button>
                  )}
                </Group>
                <Text size="xs" c="dimmed">
                  {t("profile.avatarHint")}
                </Text>
              </Stack>
            </Group>

            <TextInput
              label={t("profile.username")}
              placeholder={t("profile.usernamePlaceholder")}
              description={t("profile.usernameHint")}
              value={username}
              onChange={(e) => setUsername(e.currentTarget.value)}
            />
            <TextInput
              label={t("profile.displayName")}
              placeholder={t("profile.displayNamePlaceholder")}
              value={displayName}
              onChange={(e) => setDisplayName(e.currentTarget.value)}
            />

            <Divider label={t("profile.contactInfo")} labelPosition="left" />

            <Textarea
              label={t("profile.bio")}
              placeholder={t("profile.bioPlaceholder")}
              autosize
              minRows={2}
              maxRows={5}
              value={bio}
              onChange={(e) => setBio(e.currentTarget.value)}
            />
            <TextInput
              label={t("profile.phone")}
              placeholder={t("profile.phonePlaceholder")}
              value={phone}
              onChange={(e) => setPhone(e.currentTarget.value)}
            />
            <TextInput
              label={t("profile.website")}
              placeholder={t("profile.websitePlaceholder")}
              value={website}
              onChange={(e) => setWebsite(e.currentTarget.value)}
            />

            <Divider label={t("profile.notifications")} labelPosition="left" />

            <Radio.Group
              value={privacy}
              onChange={(value) => setPrivacy(value as NotificationPrivacy)}
              label={t("profile.lockScreen")}
              description={t("profile.lockScreenHint")}
            >
              <Stack gap="xs" mt="xs">
                <Radio
                  value="preview"
                  label={t("profile.previewMessage")}
                  description={t("profile.previewMessageHint")}
                />
                <Radio
                  value="sender"
                  label={t("profile.previewSender")}
                  description={t("profile.previewSenderHint")}
                />
                <Radio
                  value="generic"
                  label={t("profile.previewGeneric")}
                  description={t("profile.previewGenericHint")}
                />
              </Stack>
            </Radio.Group>

            <Group justify="flex-end">
              <Button onClick={save} loading={saving}>
                {t("profile.save")}
              </Button>
            </Group>
          </>
        )}

        {/* Outside the `user &&` gate on purpose: the version must still be readable when the
            profile failed to load, which is exactly when someone is filing the bug report that
            needs it. It describes the running bundle, not the account. */}
        <Divider />
        <Text size="xs" c="dimmed" ta="center">
          {t("common.version", { version: APP_VERSION })}
          {" · "}
          {BUILD_ID}
        </Text>
      </Stack>
    </Container>
  );
}
