package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type UserState struct {
	Step         string
	Order        Order
	WaitingPhoto bool
}

type Order struct {
	UserID       int64  `json:"user_id"`
	FullName     string `json:"full_name"`
	Username     string `json:"username"`
	Article      string `json:"article"`
	Size         string `json:"size"`
	Color        string `json:"color"`
	City         string `json:"city"`
	Delivery     string `json:"delivery"`
	PhotoFileID  string `json:"photo_file_id,omitempty"`
	Contact      string `json:"contact"`
	Status       string `json:"status"`
	AdminMessage string `json:"admin_message,omitempty"`
}

var (
	userStates = make(map[int64]*UserState)
	adminIDs   = []int64{1188378688, 1103525572, 993582884}
	ordersFile = "orders.json"
)

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable is not set")
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal("Error creating bot: ", err)
	}

	bot.Debug = false
	log.Printf("Бот запущен и готов к работе!")

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			chatID := update.Message.Chat.ID
			if userStates[chatID] == nil {
				userStates[chatID] = &UserState{Step: "menu"}
			}
			handleMessage(bot, update.Message)
		} else if update.CallbackQuery != nil {
			handleCallback(bot, update.CallbackQuery)
		}
	}
}

func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	chatID := message.Chat.ID
	state := userStates[chatID]

	if message.IsCommand() {
		switch message.Command() {
		case "start":
			state.Step = "menu"
			sendMenu(bot, chatID)
			return
		case "myorders":
			sendMyOrders(bot, chatID, message.From.UserName)
			return
		}
	}

	if state.WaitingPhoto && message.Photo != nil {
		photo := message.Photo[len(message.Photo)-1]
		state.Order.PhotoFileID = photo.FileID
		state.WaitingPhoto = false
		state.Step = "contact"
		msg := tgbotapi.NewMessage(chatID, "☎️ Укажи контакт для связи (Имя и Telegram или номер):")
		bot.Send(msg)
		return
	}

	switch state.Step {
	case "menu":
		sendMenu(bot, chatID)
	case "welcome":
		sendWelcome(bot, chatID)
	case "start_order":
		msg := tgbotapi.NewMessage(chatID, "🔢 Введи артикул товара:")
		bot.Send(msg)
		state.Step = "article"
	case "article":
		state.Order.Article = message.Text
		msg := tgbotapi.NewMessage(chatID, "📏 Укажи размер (EU/US/CN):")
		bot.Send(msg)
		state.Step = "size"
	case "size":
		state.Order.Size = message.Text
		msg := tgbotapi.NewMessage(chatID, "🎨 Укажи цвет/модель (если есть варианты, иначе напиши '-'):")
		bot.Send(msg)
		state.Step = "color"
	case "color":
		state.Order.Color = message.Text
		msg := tgbotapi.NewMessage(chatID, "📍 Укажи город:")
		bot.Send(msg)
		state.Step = "city"
	case "city":
		state.Order.City = message.Text
		msg := tgbotapi.NewMessage(chatID, "🚚 Укажи способ доставки:")
		bot.Send(msg)
		state.Step = "delivery"
	case "delivery":
		state.Order.Delivery = message.Text
		msg := tgbotapi.NewMessage(chatID, "Хочешь прикрепить скрин товара?")
		msg.ReplyMarkup = yesNoKeyboard()
		bot.Send(msg)
		state.Step = "want_photo"
	case "want_photo":
		msg := tgbotapi.NewMessage(chatID, "Пожалуйста, выбери вариант с помощью кнопок ниже.")
		msg.ReplyMarkup = yesNoKeyboard()
		bot.Send(msg)
	case "contact":
		state.Order.Contact = message.Text
		state.Order.UserID = chatID
		if message.From != nil {
			state.Order.Username = message.From.UserName
			state.Order.FullName = message.From.FirstName + " " + message.From.LastName
		}
		state.Order.Status = "pending"
		saveOrder(state.Order)
		msg := tgbotapi.NewMessage(chatID, "🎉 Спасибо! Мы проверим товар и пришлём тебе:\n- Итоговую цену\n- Срок доставки\n- Способы оплаты\nОбычно это занимает 10–20 минут.")
		bot.Send(msg)
		notifyAdmins(bot, state.Order)
		delete(userStates, chatID)
	case "await_admin_price":
		// Пользователь не должен писать на этом этапе
	}
}

func handleCallback(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery) {
	chatID := cb.Message.Chat.ID
	state := userStates[chatID]
	data := cb.Data

	switch state.Step {
	case "menu":
		if data == "start_order" {
			state.Step = "start_order"
			handleMessage(bot, cb.Message)
		} else if data == "myorders" {
			sendMyOrders(bot, chatID, cb.From.UserName)
		} else if data == "contact_admins" {
			sendContactAdmins(bot, chatID)
		}
	case "welcome":
		if data == "start_order" {
			state.Step = "start_order"
			handleMessage(bot, cb.Message)
		}
	case "want_photo":
		if data == "yes_photo" {
			state.WaitingPhoto = true
			msg := tgbotapi.NewMessage(chatID, "Пришли фото товара:")
			bot.Send(msg)
			state.Step = "wait_photo_upload"
		} else if data == "skip_photo" {
			state.Step = "contact"
			msg := tgbotapi.NewMessage(chatID, "☎️ Укажи контакт для связи (Имя и Telegram или номер):")
			bot.Send(msg)
		}
	case "wait_photo_upload":
		// Ждём фото, обработка в handleMessage
	case "admin_action":
		// data: admin_price:<orderUserID> или admin_reject:<orderUserID>
		if strings.HasPrefix(data, "admin_price:") {
			userIDstr := strings.TrimPrefix(data, "admin_price:")
			userID, _ := strconv.ParseInt(userIDstr, 10, 64)
			userStates[userID] = &UserState{Step: "await_admin_price"}
			msg := tgbotapi.NewMessage(cb.From.ID, "Введите сообщение для пользователя (цена, сроки, оплата):")
			bot.Send(msg)
			// Сохраняем, что следующий текст от админа — это цена для пользователя
			userStates[cb.From.ID] = &UserState{Step: "send_price", Order: Order{UserID: userID}}
		} else if strings.HasPrefix(data, "admin_reject:") {
			userIDstr := strings.TrimPrefix(data, "admin_reject:")
			userID, _ := strconv.ParseInt(userIDstr, 10, 64)
			updateOrderStatus(userID, "rejected", "")
			msg := tgbotapi.NewMessage(userID, "❌ Ваш заказ отклонён администратором.")
			bot.Send(msg)
		}
	case "user_confirm":
		if data == "user_accept" {
			updateOrderStatus(chatID, "confirmed", "")
			msg := tgbotapi.NewMessage(chatID, "🚀 Заказ принят! Мы напишем тебе, как только товар будет на складе или в пути.")
			bot.Send(msg)
		} else if data == "user_decline" {
			updateOrderStatus(chatID, "declined", "")
			msg := tgbotapi.NewMessage(chatID, "❌ Заказ отменён.")
			bot.Send(msg)
		}
	}
}

func sendWelcome(bot *tgbotapi.BotAPI, chatID int64) {
	text := "Привет!\nИнструкция к оформлению товара:\n1. Включи VPN\n2. Зайди в Poizon\n3. Найди товар и скопируй артикул\n4. Укажи размеры и доставку\n5. Мы пришлём цену и варианты"
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = startOrderKeyboard()
	bot.Send(msg)
}

func startOrderKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📨 Оформить заказ", "start_order"),
			tgbotapi.NewInlineKeyboardButtonURL("📖 Полный гайд", "https://dzen.ru/a/Z3qUVa602E7y-7zW"),
		),
	)
}

func yesNoKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Да", "yes_photo"),
			tgbotapi.NewInlineKeyboardButtonData("Пропустить", "skip_photo"),
		),
	)
}

func saveOrder(order Order) {
	orders := loadOrders()
	orders = append(orders, order)
	data, _ := json.MarshalIndent(orders, "", "  ")
	_ = ioutil.WriteFile(ordersFile, data, 0644)
}

func loadOrders() []Order {
	var orders []Order
	data, err := ioutil.ReadFile(ordersFile)
	if err == nil {
		_ = json.Unmarshal(data, &orders)
	}
	return orders
}

func sendMyOrders(bot *tgbotapi.BotAPI, chatID int64, username string) {
	orders := loadOrders()
	var sb strings.Builder
	for _, o := range orders {
		if o.UserID == chatID || o.Username == username {
			sb.WriteString(fmt.Sprintf("Товар: %s\nРазмер: %s\nЦвет: %s\nГород: %s\nСтатус: %s\n---\n", o.Article, o.Size, o.Color, o.City, o.Status))
		}
	}
	if sb.Len() == 0 {
		sb.WriteString("У вас нет заказов.")
	}
	msg := tgbotapi.NewMessage(chatID, sb.String())
	bot.Send(msg)
}

func notifyAdmins(bot *tgbotapi.BotAPI, order Order) {
	for _, adminID := range adminIDs {
		text := fmt.Sprintf("Новая заявка!\nИмя: %s\nUsername: @%s\nАртикул: %s\nРазмер: %s\nЦвет: %s\nГород: %s\nДоставка: %s\nКонтакт: %s",
			order.FullName, order.Username, order.Article, order.Size, order.Color, order.City, order.Delivery, order.Contact)
		msg := tgbotapi.NewMessage(adminID, text)
		msg.ReplyMarkup = adminActionKeyboard(order.UserID)
		bot.Send(msg)
		if order.PhotoFileID != "" {
			photoMsg := tgbotapi.NewPhoto(adminID, tgbotapi.FileID(order.PhotoFileID))
			bot.Send(photoMsg)
		}
	}
}

// Объявление прототипа функции sendMenu, чтобы не было ошибки undefined
func sendMenu(bot *tgbotapi.BotAPI, chatID int64) {
	orders := loadOrders()
	hasOrders := false
	for _, o := range orders {
		if o.UserID == chatID {
			hasOrders = true
			break
		}
	}
	if hasOrders {
		text := "👋 Снова привет!\nТы можешь:\n- 📨 Оформить новый заказ\n- 📦 Посмотреть текущие\n- 💬 Написать нам\n- 📖 Полный гайд"
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = menuKeyboard()
		bot.Send(msg)
	} else {
		sendWelcome(bot, chatID)
	}
}

func menuKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📨 Оформить заказ", "start_order"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📦 Мои заказы", "myorders"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💬 Написать нам", "contact_admins"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("📖 Полный гайд", "https://dzen.ru/a/Z3qUVa602E7y-7zW"),
		),
	)
}

func sendContactAdmins(bot *tgbotapi.BotAPI, chatID int64) {
	var sb strings.Builder
	sb.WriteString("Связаться с админами:\n")
	for _, id := range adminIDs {
		sb.WriteString(fmt.Sprintf("- @admin%d\n", id)) // Можно заменить на реальные username
	}
	msg := tgbotapi.NewMessage(chatID, sb.String())
	bot.Send(msg)
}

func adminActionKeyboard(userID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Отправить цену", fmt.Sprintf("admin_price:%d", userID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отклонить", fmt.Sprintf("admin_reject:%d", userID)),
		),
	)
}

func updateOrderStatus(userID int64, status, adminMsg string) {
	orders := loadOrders()
	for i, o := range orders {
		if o.UserID == userID && o.Status == "pending" {
			orders[i].Status = status
			if adminMsg != "" {
				orders[i].AdminMessage = adminMsg
			}
		}
	}
	data, _ := json.MarshalIndent(orders, "", "  ")
	_ = ioutil.WriteFile(ordersFile, data, 0644)
	if status == "price_sent" && adminMsg != "" {
		msg := tgbotapi.NewMessage(userID, adminMsg)
		msg.ReplyMarkup = userConfirmKeyboard()
		// Нужно получить bot, но тут нет доступа. Лучше вызывать из handleMessage.
	}
}

func userConfirmKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Подтвердить", "user_accept"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отказаться", "user_decline"),
		),
	)
}
