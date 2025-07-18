package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"
	"database/sql"

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

	// Проверка существования таблицы
	var tableExists bool
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='users')").Scan(&tableExists)
	if err != nil {
		log.Fatal("Failed to check table existence:", err)
	}

	if !tableExists {
		// Создание таблицы с правильной структурой
		createTable := `
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER UNIQUE,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			birthdate TEXT,          -- Дата рождения
			weekly_km TEXT,
			trainings_per_week INTEGER,
			age_group TEXT,
			best_time_5k TEXT,
			best_time_10k TEXT,
			best_time_21k TEXT,
			best_time_42k TEXT,
			plan_duration INTEGER,
			delivery_option TEXT,
			vdot_max REAL,           -- Максимальное значение VDOT
			vdot_correction REAL,    -- Корректировка для VDOT
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`

		if _, err = db.Exec(createTable); err != nil {
			log.Fatal("Failed to create table:", err)
		}
	} else {
		// Добавление новых колонок
		columnsToAdd := []struct {
			name       string
			definition string
		}{
			{"vdot_max", "REAL"},            // Добавляем поле для максимального значения VDOT
			{"vdot_correction", "REAL"},     // Добавляем поле для корректировки VDOT
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

	// Проверка на пустое значение для vdot_max и vdot_correction (можно задать дефолтное значение, если нужно)
	if _, ok := data["vdot_max"]; !ok {
		data["vdot_max"] = 40.0 // Значение по умолчанию для VDOT
	}
	if _, ok := data["vdot_correction"]; !ok {
		data["vdot_correction"] = 0.0 // Значение по умолчанию для корректировки VDOT
	}

	_, err := db.Exec(`
		INSERT OR REPLACE INTO users (
			chat_id, username, first_name, last_name,
			birthdate, weekly_km, trainings_per_week, age_group,
			best_time_5k, best_time_10k, best_time_21k, best_time_42k,
			plan_duration, delivery_option, vdot_max, vdot_correction, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		chatID,
		data["username"],
		data["first_name"],
		data["last_name"],
		data["birthdate"],      // Дата рождения
		data["weekly_km"],
		data["trainings_per_week"],
		data["age_group"],
		data["best_time_5k"],
		data["best_time_10k"],
		data["best_time_21k"],
		data["best_time_42k"],
		data["plan_duration"],
		data["delivery_option"],
		data["vdot_max"],       // Максимальное значение VDOT
		data["vdot_correction"],// Корректировка для VDOT
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

	// Проверка формата MM:SS
	_, err := time.Parse("04:05", timeStr)
	if err == nil {
		return true
	}

	// Проверка формата HH:MM:SS для марафона
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

func handleCallback(bot *tgbotapi.BotAPI, callback *tgbotapi.CallbackQuery) {
	chatID := callback.Message.Chat.ID
	data := callback.Data

	state := userData[chatID]

	log.Printf("[Callback] chatID=%d, data=%s", chatID, data)

	// Обрабатываем нажатие кнопок
	switch data {
	case "download_plan":
		if state["plan_text"] == nil {
			bot.Send(tgbotapi.NewMessage(chatID, "План пока не сформирован. Пожалуйста, завершите ввод данных."))
			return
		}
		// Здесь будет код для генерации и отправки PDF
		bot.Send(tgbotapi.NewMessage(chatID, "Ваш план будет готов через несколько минут..."))
	case "subscribe_weekly":
		if state["subscribed"] != nil && state["subscribed"] == true {
			bot.Send(tgbotapi.NewMessage(chatID, "Вы уже подписаны на еженедельную рассылку."))
			return
		}
		state["subscribed"] = true
		state["subscribe_time"] = time.Now()
		saveUserData(chatID)
		bot.Send(tgbotapi.NewMessage(chatID, "Вы успешно подписались на еженедельную рассылку!"))
		return
	default:
		// Остальная логика для обработки выбора данных
	}
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

		// Основная логика бота
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
		}
	}
}
