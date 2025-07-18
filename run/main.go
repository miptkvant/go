package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB
var userState = make(map[int64]string)
var userData = make(map[int64]map[string]interface{})

func initDB() {
	var err error
	db, err = sql.Open("sqlite3", "./training_bot.db")
	if err != nil {
		log.Fatal(err)
	}

	// Сначала проверяем существование таблицы
	var tableExists bool
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='users')").Scan(&tableExists)
	if err != nil {
		log.Fatal("Failed to check table existence:", err)
	}

	if !tableExists {
		// Создаем таблицу с правильной структурой
		createTable := `
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER UNIQUE,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			weekly_km TEXT,
			trainings_per_week INTEGER,
			age_group TEXT,
			best_time_5k TEXT,
			best_time_10k TEXT,
			best_time_21k TEXT,
			best_time_42k TEXT,
			plan_duration INTEGER,
			delivery_option TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`

		if _, err = db.Exec(createTable); err != nil {
			log.Fatal("Failed to create table:", err)
		}
	} else {
		// Добавляем недостающие колонки, если таблица уже существует
		columnsToAdd := []struct {
			name       string
			definition string
		}{
			{"username", "TEXT"},
			{"first_name", "TEXT"},
			{"last_name", "TEXT"},
			{"weekly_km", "TEXT"},
			{"trainings_per_week", "INTEGER"},
			{"age_group", "TEXT"},
			{"best_time_5k", "TEXT"},
			{"best_time_10k", "TEXT"},
			{"best_time_21k", "TEXT"},
			{"best_time_42k", "TEXT"},
			{"plan_duration", "INTEGER"},
			{"delivery_option", "TEXT"},
			{"created_at", "TIMESTAMP DEFAULT CURRENT_TIMESTAMP"},
		}

		for _, column := range columnsToAdd {
			_, err = db.Exec(fmt.Sprintf(`
				ALTER TABLE users ADD COLUMN %s %s;
			`, column.name, column.definition))
			// Игнорируем ошибку, если колонка уже существует
			if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
				log.Printf("Warning: could not add column %s: %v", column.name, err)
			}
		}
	}
}

func saveUserData(chatID int64) error {
	data := userData[chatID]

	// Убедимся, что created_at всегда имеет значение
	if _, ok := data["created_at"]; !ok {
		data["created_at"] = time.Now()
	}

	_, err := db.Exec(`
		INSERT OR REPLACE INTO users (
			chat_id, username, first_name, last_name,
			weekly_km, trainings_per_week, age_group,
			best_time_5k, best_time_10k, best_time_21k, best_time_42k,
			plan_duration, delivery_option, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		chatID,
		data["username"],
		data["first_name"],
		data["last_name"],
		data["weekly_km"],
		data["trainings_per_week"],
		data["age_group"],
		data["best_time_5k"],
		data["best_time_10k"],
		data["best_time_21k"],
		data["best_time_42k"],
		data["plan_duration"],
		data["delivery_option"],
		data["created_at"],
	)

	if err != nil {
		return fmt.Errorf("error saving user data: %v", err)
	}
	return nil
}

func validateTimeFormat(timeStr string) bool {
	if timeStr == "Я не знаю" {
		return true
	}

	// Проверяем формат MM:SS
	_, err := time.Parse("04:05", timeStr)
	if err == nil {
		return true
	}

	// Проверяем формат HH:MM:SS для марафона
	_, err = time.Parse("15:04:05", timeStr)
	return err == nil
}

func createTimeKeyboard() tgbotapi.ReplyKeyboardMarkup {
	return tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("Я не знаю"),
		),
	)
}

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable not set")
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	bot.Debug = true
	initDB()

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		chatID := update.Message.Chat.ID
		text := strings.TrimSpace(update.Message.Text)

		// Инициализация данных пользователя
		if _, ok := userData[chatID]; !ok {
			userData[chatID] = map[string]interface{}{
				"username":   update.Message.From.UserName,
				"first_name": update.Message.From.FirstName,
				"last_name":  update.Message.From.LastName,
				"created_at": time.Now(),
			}
		}

		switch userState[chatID] {
		case "":
			if strings.ToLower(text) == "привет" || strings.ToLower(text) == "/start" {
				name := update.Message.From.FirstName
				if name == "" {
					name = update.Message.From.UserName
				}
				msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Привет, %s! Я помогу тебе написать тренировочный план. Для этого ответь на несколько вопросов.", name))
				bot.Send(msg)

				// Первый вопрос
				msg = tgbotapi.NewMessage(chatID, "Сколько км в неделю вы хотите пробегать?")
				msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
					tgbotapi.NewKeyboardButtonRow(
						tgbotapi.NewKeyboardButton("до 30 км"),
						tgbotapi.NewKeyboardButton("30-70 км"),
					),
					tgbotapi.NewKeyboardButtonRow(
						tgbotapi.NewKeyboardButton("более 70 км"),
					),
				)
				userState[chatID] = "awaiting_weekly_km"
				bot.Send(msg)
			}

		case "awaiting_weekly_km":
			validOptions := map[string]bool{"до 30 км": true, "30-70 км": true, "более 70 км": true}
			if !validOptions[text] {
				msg := tgbotapi.NewMessage(chatID, "Пожалуйста, выберите один из предложенных вариантов.")
				bot.Send(msg)
				continue
			}

			userData[chatID]["weekly_km"] = text

			// Второй вопрос
			msg := tgbotapi.NewMessage(chatID, "Сколько тренировок в неделю вы хотите?")
			msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("2"),
					tgbotapi.NewKeyboardButton("3"),
					tgbotapi.NewKeyboardButton("4"),
				),
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("5"),
					tgbotapi.NewKeyboardButton("6"),
				),
			)
			userState[chatID] = "awaiting_trainings_per_week"
			bot.Send(msg)

		case "awaiting_trainings_per_week":
			validOptions := map[string]bool{"2": true, "3": true, "4": true, "5": true, "6": true}
			if !validOptions[text] {
				msg := tgbotapi.NewMessage(chatID, "Пожалуйста, выберите число от 2 до 6.")
				bot.Send(msg)
				continue
			}

			userData[chatID]["trainings_per_week"] = text

			// Третий вопрос
			msg := tgbotapi.NewMessage(chatID, "Ваш возраст?")
			msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("до 18 лет"),
					tgbotapi.NewKeyboardButton("18-40 лет"),
				),
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("40-60 лет"),
					tgbotapi.NewKeyboardButton("больше 60 лет"),
				),
			)
			userState[chatID] = "awaiting_age_group"
			bot.Send(msg)

		case "awaiting_age_group":
			validOptions := map[string]bool{"до 18 лет": true, "18-40 лет": true, "40-60 лет": true, "больше 60 лет": true}
			if !validOptions[text] {
				msg := tgbotapi.NewMessage(chatID, "Пожалуйста, выберите один из предложенных вариантов.")
				bot.Send(msg)
				continue
			}

			userData[chatID]["age_group"] = text

			// Четвертый вопрос
			msg := tgbotapi.NewMessage(chatID, "Ваше лучшее время на дистанции 5 км? (в формате ММ:СС или нажмите «Я не знаю»)")
			msg.ReplyMarkup = createTimeKeyboard()
			userState[chatID] = "awaiting_best_time_5k"
			bot.Send(msg)

		case "awaiting_best_time_5k":
			if !validateTimeFormat(text) {
				msg := tgbotapi.NewMessage(chatID, "Пожалуйста, введите время в формате ММ:СС (например, 25:30) или нажмите «Я не знаю».")
				msg.ReplyMarkup = createTimeKeyboard()
				bot.Send(msg)
				continue
			}

			userData[chatID]["best_time_5k"] = text

			// Пятый вопрос
			msg := tgbotapi.NewMessage(chatID, "Ваше лучшее время на дистанции 10 км? (в формате ММ:СС или нажмите «Я не знаю»)")
			msg.ReplyMarkup = createTimeKeyboard()
			userState[chatID] = "awaiting_best_time_10k"
			bot.Send(msg)

		case "awaiting_best_time_10k":
			if !validateTimeFormat(text) {
				msg := tgbotapi.NewMessage(chatID, "Пожалуйста, введите время в формате ММ:СС (например, 52:15) или нажмите «Я не знаю».")
				msg.ReplyMarkup = createTimeKeyboard()
				bot.Send(msg)
				continue
			}

			userData[chatID]["best_time_10k"] = text

			// Шестой вопрос
			msg := tgbotapi.NewMessage(chatID, "Ваше лучшее время на дистанции 21 км (полумарафон)? (в формате ЧЧ:ММ:СС или нажмите «Я не знаю»)")
			msg.ReplyMarkup = createTimeKeyboard()
			userState[chatID] = "awaiting_best_time_21k"
			bot.Send(msg)

		case "awaiting_best_time_21k":
			if !validateTimeFormat(text) {
				msg := tgbotapi.NewMessage(chatID, "Пожалуйста, введите время в формате ЧЧ:ММ:СС (например, 1:45:30) или нажмите «Я не знаю».")
				msg.ReplyMarkup = createTimeKeyboard()
				bot.Send(msg)
				continue
			}

			userData[chatID]["best_time_21k"] = text

			// Седьмой вопрос
			msg := tgbotapi.NewMessage(chatID, "Ваше лучшее время на дистанции 42 км (марафон)? (в формате ЧЧ:ММ:СС или нажмите «Я не знаю»)")
			msg.ReplyMarkup = createTimeKeyboard()
			userState[chatID] = "awaiting_best_time_42k"
			bot.Send(msg)

		case "awaiting_best_time_42k":
			if !validateTimeFormat(text) {
				msg := tgbotapi.NewMessage(chatID, "Пожалуйста, введите время в формате ЧЧ:ММ:СС (например, 3:45:30) или нажмите «Я не знаю».")
				msg.ReplyMarkup = createTimeKeyboard()
				bot.Send(msg)
				continue
			}

			userData[chatID]["best_time_42k"] = text

			// Восьмой вопрос
			msg := tgbotapi.NewMessage(chatID, "На какой срок пишем план (в месяцах)?")
			msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("1"),
					tgbotapi.NewKeyboardButton("2"),
					tgbotapi.NewKeyboardButton("3"),
				),
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("4"),
					tgbotapi.NewKeyboardButton("5"),
					tgbotapi.NewKeyboardButton("6"),
				),
			)
			userState[chatID] = "awaiting_plan_duration"
			bot.Send(msg)

		case "awaiting_plan_duration":
			validOptions := map[string]bool{"1": true, "2": true, "3": true, "4": true, "5": true, "6": true}
			if !validOptions[text] {
				msg := tgbotapi.NewMessage(chatID, "Пожалуйста, выберите число от 1 до 6.")
				bot.Send(msg)
				continue
			}

			userData[chatID]["plan_duration"] = text

			// Девятый вопрос
			msg := tgbotapi.NewMessage(chatID, "Хотите скачать план или подписаться на рассылку?")
			msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("Скачать план"),
					tgbotapi.NewKeyboardButton("Подписаться на рассылку"),
				),
			)
			userState[chatID] = "awaiting_delivery_option"
			bot.Send(msg)

		case "awaiting_delivery_option":
			validOptions := map[string]bool{"Скачать план": true, "Подписаться на рассылку": true}
			if !validOptions[text] {
				msg := tgbotapi.NewMessage(chatID, "Пожалуйста, выберите один из предложенных вариантов.")
				bot.Send(msg)
				continue
			}

			userData[chatID]["delivery_option"] = text

			// Сохраняем все данные
			if err := saveUserData(chatID); err != nil {
				msg := tgbotapi.NewMessage(chatID, "Ошибка при сохранении данных. Пожалуйста, попробуйте позже.")
				bot.Send(msg)
				log.Printf("Error saving user data: %v", err)
				continue
			}

			// Завершение опроса
			msg := tgbotapi.NewMessage(chatID, "Спасибо! Ваши ответы сохранены.")
			msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
			bot.Send(msg)

			// Здесь можно добавить логику для генерации плана или подписки
			if text == "Скачать план" {
				// Генерация и отправка плана
				msg := tgbotapi.NewMessage(chatID, "Ваш план будет готов через несколько минут...")
				bot.Send(msg)
			} else {
				// Подписка на рассылку
				msg := tgbotapi.NewMessage(chatID, "Вы подписаны на еженедельную рассылку тренировочных планов!")
				bot.Send(msg)
			}

			// Сброс состояния
			delete(userState, chatID)
			delete(userData, chatID)
		}
	}
}
